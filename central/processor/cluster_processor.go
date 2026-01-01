// central/processor/cluster_processor.go
package processor

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"central/config"
	"central/core"
	"central/notifier"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

func ProcessCluster(wg *sync.WaitGroup, central *core.Central, acct config.AccountConfig, cluster *config.ClusterConfig) {
	defer wg.Done()

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(acct.AccessKey, acct.SecretKey, "")),
		awsconfig.WithRegion(cluster.Region),
	)
	if err != nil {
		log.Printf("AWS config failed for cluster %s: %v", cluster.Name, err)
		return
	}

	eksClient := eks.NewFromConfig(awsCfg)
	asgClient := autoscaling.NewFromConfig(awsCfg)

	lastScaleDown := make(map[string]time.Time)

	ch := central.GetClusterChan(cluster.Name)
	for req := range ch {
		for _, ng := range req.NodeGroups {
			if len(cluster.NodeGroups) > 0 && !contains(cluster.NodeGroups, ng.Name) {
				continue
			}

			avgUtil := avgUtil(ng.NodeUtils)
			if avgUtil == 0 {
				continue
			}

			if avgUtil > float64(cluster.HighThreshold)/100 {
				scaleUp(eksClient, cluster.Name, ng)
				notifier.Send(
					fmt.Sprintf("[↑] %s/%s scaled up", cluster.Name, ng.Name),
					central.GetTelegramChatIDs(),
				)
				continue
			}

			if avgUtil < float64(cluster.LowThreshold)/100 && ng.DesiredSize > ng.MinSize {
				if time.Since(lastScaleDown[ng.Name]) < time.Duration(cluster.CooldownSeconds)*time.Second {
					continue
				}

				lowNodeName := selectLowNode(ng.NodeUtils, avgUtil)
				if lowNodeName == "" {
					continue
				}

				var instanceID string
				for _, node := range ng.Nodes {
					if node.Name == lowNodeName {
						instanceID = node.InstanceId
						break
					}
				}
				if instanceID == "" || !simulateRemoval(ng.Nodes, lowNodeName, cluster.MaxThreshold) {
					continue
				}

				terminateInstance(asgClient, ng.AsgName, instanceID)
				lastScaleDown[ng.Name] = time.Now()
				notifier.Send(
					fmt.Sprintf("[↓] %s/%s scaled down (terminated %s)", cluster.Name, ng.Name, instanceID),
					central.GetTelegramChatIDs(),
				)
			}
		}
	}
}

func avgUtil(utils map[string]float64) float64 {
	if len(utils) == 0 {
		return 0
	}
	var sum float64
	for _, u := range utils {
		sum += u
	}
	return sum / float64(len(utils))
}

func selectLowNode(utils map[string]float64, avg float64) string {
	var low string
	var min = 1.0
	for name, u := range utils {
		if u < avg && u < min {
			min = u
			low = name
		}
	}
	return low
}

func simulateRemoval(nodes []core.NodeInfo, lowNode string, maxThreshold int) bool {
	var totalReq int64
	for _, n := range nodes {
		totalReq += n.RequestCpuMilli
	}
	remaining := len(nodes) - 1
	if remaining == 0 {
		return false
	}
	avgReq := totalReq / int64(remaining)
	for _, n := range nodes {
		if n.Name == lowNode {
			continue
		}
		if float64(avgReq)/float64(n.AllocatableCpuMilli) > float64(maxThreshold)/100 {
			return false
		}
	}
	return true
}

func scaleUp(client *eks.Client, clusterName string, ng core.NodeGroupData) {
	newDesired := ng.DesiredSize + 1
	if newDesired > ng.MaxSize {
		return
	}
	input := &eks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String(clusterName),
		NodegroupName: aws.String(ng.Name),
		ScalingConfig: &types.NodegroupScalingConfig{
			DesiredSize: aws.Int32(newDesired),
		},
	}
	_, _ = client.UpdateNodegroupConfig(context.Background(), input)
}

func terminateInstance(client *autoscaling.Client, asgName, instanceID string) {
	input := &autoscaling.TerminateInstanceInAutoScalingGroupInput{
		InstanceId:                     aws.String(instanceID),
		ShouldDecrementDesiredCapacity: aws.Bool(true),
	}
	_, _ = client.TerminateInstanceInAutoScalingGroup(context.Background(), input)
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}