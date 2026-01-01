// central/processor/cluster_processor.go
package processor

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"central"
	"central/config"
	"central/notifier"
	"central/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
	pb "path/to/proto"
)

func ProcessCluster(wg *sync.WaitGroup, central *central.Central, acct config.AccountConfig, cluster *config.ClusterConfig) {
	defer wg.Done()

	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(acct.AccessKey, acct.SecretKey, "")),
		config.WithRegion(cluster.Region),
	)
	if err != nil {
		log.Printf("AWS config for %s failed: %v", cluster.Name, err)
		return
	}
	eksClient := eks.NewFromConfig(awsCfg)
	asgClient := autoscaling.NewFromConfig(awsCfg)

	lastScaleDown := make(map[string]time.Time) // per nodegroup

	ch := central.clusterChans[cluster.Name]
	for req := range ch {
		// 存储上报数据
		if err := storage.StoreReport(cluster.Name, req); err != nil {
			log.Printf("Store report for %s failed: %v", cluster.Name, err)
		}

		// 业务处理: 遍历 nodegroups (假设 req 包含 NodeGroups []NodeGroupData, 每个有 NodeUtils map[string]float64, Nodes []NodeInfo 等)
		for _, ngData := range req.NodeGroups {
			ngName := ngData.Name
			if len(cluster.NodeGroups) > 0 && !contains(cluster.NodeGroups, ngName) {
				continue
			}

			// 计算 avgUtil, nodeUtils (假设上报数据已包含, 或重新计算)
			avgUtil := calculateAvgUtil(ngData.NodeUtils)
			if avgUtil == 0 {
				continue
			}

			if avgUtil > float64(cluster.HighThreshold)/100 {
				scaleUp(eksClient, ngData, cluster)
				notifier.Send(fmt.Sprintf("Cluster %s NG %s scaled up", cluster.Name, ngName), central.cfg.Telegram.ChatIDs)
				continue
			}

			if avgUtil < float64(cluster.LowThreshold)/100 && ngData.DesiredSize > ngData.MinSize {
				if time.Since(lastScaleDown[ngName]) < time.Duration(cluster.CooldownSeconds)*time.Second {
					continue
				}
				lowNode := selectLowUtilNode(ngData.NodeUtils, avgUtil)
				if lowNode == "" || !isImbalanced(ngData.NodeUtils, avgUtil, cluster.MaxThreshold) {
					continue
				}
				if simulateRemoval(ngData.Nodes, lowNode, cluster.MaxThreshold) {
					// 执行缩容: 终结实例 (假设 Agent 已 drain, 这里只 term)
					terminateInstance(asgClient, ngData.AsgName, lowNode.InstanceID)
					lastScaleDown[ngName] = time.Now()
					notifier.Send(fmt.Sprintf("Cluster %s NG %s scaled down, terminated %s", cluster.Name, ngName, lowNode.InstanceID), central.cfg.Telegram.ChatIDs)
				}
			}
		}
	}
}

// 辅助函数 (基于原 autoscaler 逻辑, 假设 pb.ReportRequest 包含必要数据)
func calculateAvgUtil(nodeUtils map[string]float64) float64 {
	var total float64
	for _, u := range nodeUtils {
		total += u
	}
	if len(nodeUtils) == 0 {
		return 0
	}
	return total / float64(len(nodeUtils))
}

func isImbalanced(nodeUtils map[string]float64, avgUtil float64, maxThreshold int) bool {
	hasLow := false
	for _, u := range nodeUtils {
		if u > float64(maxThreshold)/100 {
			return false
		}
		if u < avgUtil {
			hasLow = true
		}
	}
	return hasLow
}

func selectLowUtilNode(nodeUtils map[string]float64, avgUtil float64) LowNode {
	var minUtil float64 = 1.0
	var lowNode LowNode
	for name, u := range nodeUtils {
		if u < avgUtil && u < minUtil {
			minUtil = u
			lowNode = LowNode{Name: name, InstanceID: "from req data"} // 假设
		}
	}
	return lowNode
}

func simulateRemoval(nodes []NodeInfo, lowNode LowNode, maxThreshold int) bool {
	// 原逻辑, 假设 NodeInfo 有 Req, Alloc
	var totalReq int64
	for _, n := range nodes {
		totalReq += n.TotalReq
	}
	remaining := len(nodes) - 1
	if remaining == 0 {
		return false
	}
	avgReq := totalReq / int64(remaining)
	for _, n := range nodes {
		if n.Name == lowNode.Name {
			continue
		}
		if float64(avgReq)/float64(n.Alloc) > float64(maxThreshold)/100 {
			return false
		}
	}
	return true
}

func scaleUp(eksClient *eks.Client, ngData NGData, cluster *config.ClusterConfig) {
	newDesired := ngData.DesiredSize + 1
	if newDesired > ngData.MaxSize {
		return
	}
	input := &eks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String(cluster.Name),
		NodegroupName: aws.String(ngData.Name),
		ScalingConfig: &types.NodegroupScalingConfig{
			DesiredSize: aws.Int32(newDesired),
		},
	}
	eksClient.UpdateNodegroupConfig(context.Background(), input)
}

func terminateInstance(asgClient *autoscaling.Client, asgName, instanceID string) {
	input := &autoscaling.TerminateInstanceInAutoScalingGroupInput{
		InstanceId:                     aws.String(instanceID),
		ShouldDecrementDesiredCapacity: aws.Bool(true),
	}
	asgClient.TerminateInstanceInAutoScalingGroup(context.Background(), input)
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// 假设类型 (根据 proto 调整)
type NGData struct {
	Name        string
	AsgName     string
	MinSize     int32
	MaxSize     int32
	DesiredSize int32
	NodeUtils   map[string]float64
	Nodes       []NodeInfo
}

type NodeInfo struct {
	Name     string
	TotalReq int64
	Alloc    int64
}

type LowNode struct {
	Name        string
	InstanceID  string
}