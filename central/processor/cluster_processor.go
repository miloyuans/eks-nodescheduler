// central/processor/cluster_processor.go
package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"central/config"
	"central/core"
	"central/model"
	"central/notifier"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

const (
	restartEndpoint     = "/restart"        // Agent 监听的重启指令 endpoint
	restartFeedbackEndpoint = "/restart-feedback" // Agent 反馈 endpoint
	restartTimeout      = 30 * time.Minute   // 等待 Agent 重启完成超时
)

func ProcessCluster(ctx context.Context, wg *sync.WaitGroup, central *core.Central, acct config.AccountConfig, cluster *config.ClusterConfig) {
	defer wg.Done()

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(acct.AccessKey, acct.SecretKey, "")),
		awsconfig.WithRegion(cluster.Region),
	)
	if err != nil {
		log.Printf("[ERROR] AWS config failed for cluster %s: %v", cluster.Name, err)
		return
	}

	eksClient := eks.NewFromConfig(awsCfg)
	asgClient := autoscaling.NewFromConfig(awsCfg)

	// 启动每日 nodegroup 管理（创建今天/明天空组 + 清理历史空组）
	go manageDailyNodeGroups(ctx, eksClient, cluster)

	lastScaleDown := make(map[string]time.Time)

	ch := central.GetClusterChan(cluster.Name)
	for {
		select {
		case req := <-ch:
			log.Printf("[INFO] Received report from cluster %s with %d nodegroups", req.ClusterName, len(req.NodeGroups))

			// 统一获取今天和明天 nodegroup 名称
			todayNG := getNodeGroupName(cluster, time.Now())
			tomorrowNG := getNodeGroupName(cluster, time.Now().Add(24*time.Hour))

			// 计算当前总节点数和总请求
			totalNodes, totalRequestCpu := calculateClusterLoad(req.NodeGroups)

			avgUtil := 0.0
			if totalNodes > 0 {
				avgUtil = float64(totalRequestCpu) / float64(totalNodes*1000) // 假设每节点 1000 milli 可分配，简化
			}

			if avgUtil > float64(cluster.HighThreshold)/100 {
				// 扩容逻辑：计算需要添加多少节点
				addCount := calculateScaleUpCount(avgUtil, totalNodes, cluster.UtilThreshold)
				if addCount > 0 {
					scaleNodeGroup(eksClient, cluster.Name, tomorrowNG, addCount) // 扩容到明天组
					notifier.Send(fmt.Sprintf("[↑] Cluster %s scaled up by %d nodes (tomorrow group %s)", cluster.Name, addCount, tomorrowNG), central.GetTelegramChatIDs())
				}
				continue
			}

			if avgUtil < float64(cluster.LowThreshold)/100 {
				if time.Since(lastScaleDown["cluster"]) < time.Duration(cluster.CooldownSeconds)*time.Second {
					continue
				}

				// 缩容逻辑
				reduceCount := calculateScaleDownCount(totalNodes, avgUtil)
				if reduceCount > 0 {
					if err := performScaleDown(ctx, eksClient, asgClient, cluster, req, tomorrowNG, reduceCount); err != nil {
						log.Printf("[ERROR] Scale down failed for %s: %v", cluster.Name, err)
						notifier.Send(fmt.Sprintf("[FAILED] Scale down failed for %s: %v", cluster.Name, err), central.GetTelegramChatIDs())
					} else {
						lastScaleDown["cluster"] = time.Now()
					}
				}
			}
		case <-ctx.Done():
			log.Printf("[INFO] Processor for cluster %s shutting down", cluster.Name)
			return
		}
	}
}

// getNodeGroupName 生成 nodegroup 名称：prefix + YYYYMMDD
func getNodeGroupName(cluster *config.ClusterConfig, t time.Time) string {
	return cluster.NodeGroupPrefix + t.Format("20060102")
}

// manageDailyNodeGroups 每天创建今天和明天空 nodegroup，并清理历史空组
func manageDailyNodeGroups(ctx context.Context, client *eks.Client, cluster *config.ClusterConfig) {
	ticker := time.NewTicker(6 * time.Hour) // 每6小时检查一次
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			today := getNodeGroupName(cluster, time.Now())
			tomorrow := getNodeGroupName(cluster, time.Now().Add(24*time.Hour))

			createEmptyNodeGroupIfNotExist(client, cluster, today)
			createEmptyNodeGroupIfNotExist(client, cluster, tomorrow)

			cleanupOldEmptyNodeGroups(client, cluster)
		}
	}
}

func createEmptyNodeGroupIfNotExist(client *eks.Client, cluster *config.ClusterConfig, ngName string) {
	_, err := client.DescribeNodegroup(context.Background(), &eks.DescribeNodegroupInput{
		ClusterName:   aws.String(cluster.Name),
		NodegroupName: aws.String(ngName),
	})
	if err == nil {
		return // 已存在
	}

	log.Printf("[INFO] Creating empty nodegroup %s for cluster %s", ngName, cluster.Name)
	input := &eks.CreateNodegroupInput{
		ClusterName:   aws.String(cluster.Name),
		NodegroupName: aws.String(ngName),
		ScalingConfig: &types.NodegroupScalingConfig{
			MinSize:     aws.Int32(0),
			MaxSize:     aws.Int32(20),
			DesiredSize: aws.Int32(0),
		},
		InstanceTypes: []string{cluster.InstanceType},
		DiskSize:      aws.Int32(int32(cluster.DiskSize)),
		AmiType:       types.AMIFamily(cluster.AmiType),
		NodeRole:      aws.String(cluster.IamRole),
	}

	_, err = client.CreateNodegroup(context.Background(), input)
	if err != nil {
		log.Printf("[ERROR] Failed to create nodegroup %s: %v", ngName, err)
		notifier.Send(fmt.Sprintf("[ERROR] Failed to create nodegroup %s in %s", ngName, cluster.Name), nil)
	} else {
		notifier.Send(fmt.Sprintf("[CREATED] Empty nodegroup %s created for %s", ngName, cluster.Name), nil)
	}
}

func cleanupOldEmptyNodeGroups(client *eks.Client, cluster *config.ClusterConfig) {
	// 列出所有 nodegroup，删除 DesiredSize=0 且非今天/明天的
	// 简化实现：遍历最近7天
	for i := 2; i <= 7; i++ {
		oldDate := time.Now().AddDate(0, 0, -i)
		oldNG := getNodeGroupName(cluster, oldDate)

		desc, err := client.DescribeNodegroup(context.Background(), &eks.DescribeNodegroupInput{
			ClusterName:   aws.String(cluster.Name),
			NodegroupName: aws.String(oldNG),
		})
		if err != nil {
			continue // 不存在或错误，跳过
		}

		if desc.Nodegroup != nil && desc.Nodegroup.ScalingConfig != nil && *desc.Nodegroup.ScalingConfig.DesiredSize == 0 {
			log.Printf("[CLEANUP] Deleting empty old nodegroup %s", oldNG)
			_, err = client.DeleteNodegroup(context.Background(), &eks.DeleteNodegroupInput{
				ClusterName:   aws.String(cluster.Name),
				NodegroupName: aws.String(oldNG),
			})
			if err != nil {
				log.Printf("[ERROR] Failed to delete old nodegroup %s: %v", oldNG, err)
			}
		}
	}
}

// calculateClusterLoad 计算集群总节点数和总 CPU 请求
func calculateClusterLoad(nodeGroups []model.NodeGroupData) (totalNodes int, totalRequest int64) {
	for _, ng := range nodeGroups {
		totalNodes += len(ng.Nodes)
		for _, node := range ng.Nodes {
			totalRequest += node.RequestCpuMilli
		}
	}
	return
}

// calculateScaleUpCount 计算需要添加多少节点
func calculateScaleUpCount(currentAvgUtil float64, currentNodes int, targetUtil float64) int {
	if currentNodes == 0 {
		return 1
	}
	add := 1
	for {
		newAvg := currentAvgUtil * float64(currentNodes) / float64(currentNodes+add)
		if newAvg <= targetUtil {
			return add
		}
		add++
		if add > 10 { // 防止无限循环
			return add
		}
	}
}

// scaleNodeGroup 调整 nodegroup DesiredSize
func scaleNodeGroup(client *eks.Client, clusterName, ngName string, delta int32) {
	input := &eks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String(clusterName),
		NodegroupName: aws.String(ngName),
		ScalingConfig: &types.NodegroupScalingConfig{
			DesiredSize: aws.Int32(delta), // delta 为绝对值或增量
		},
	}
	_, _ = client.UpdateNodegroupConfig(context.Background(), input)
}

// performScaleDown 执行缩容流程
func performScaleDown(ctx context.Context, eksClient *eks.Client, asgClient *autoscaling.Client, cluster *config.ClusterConfig, req model.ReportRequest, tomorrowNG string, reduceCount int) error {
	// 1. 将多余节点“迁移”到明天组（增加 DesiredSize）
	scaleNodeGroup(eksClient, cluster.Name, tomorrowNG, int32(reduceCount))

	// 2. Cordon 所有非明天组的节点
	for _, ng := range req.NodeGroups {
		if strings.Contains(ng.Name, tomorrowNG) {
			continue
		}
		for _, node := range ng.Nodes {
			// 假设有 k8s client 可以 cordon，这里模拟日志
			log.Printf("[CORDON] Cordon node %s in old group %s", node.Name, ng.Name)
		}
	}

	// 3. 下发重启指令给 Agent
	if err := sendRestartCommand(cluster); err != nil {
		return err
	}

	// 4. 等待 Agent 反馈
	if err := waitForRestartFeedback(cluster); err != nil {
		return err
	}

	// 5. 删除旧空 nodegroup
	for _, ng := range req.NodeGroups {
		if !strings.Contains(ng.Name, tomorrowNG) && ng.DesiredSize == 0 {
			_, _ = eksClient.DeleteNodegroup(context.Background(), &eks.DeleteNodegroupInput{
				ClusterName:   aws.String(cluster.Name),
				NodegroupName: aws.String(ng.Name),
			})
			log.Printf("[DELETED] Deleted old empty nodegroup %s", ng.Name)
		}
	}

	notifier.Send(fmt.Sprintf("[↓] Cluster %s scaled down by %d nodes", cluster.Name, reduceCount), nil)
	return nil
}

// sendRestartCommand 下发重启指令给 Agent
func sendRestartCommand(cluster *config.ClusterConfig) error {
	// 假设 Agent 监听在 cluster.AgentEndpoint (从配置扩展)
	// 示例 POST /restart
	resp, err := http.Post(cluster.AgentEndpoint+restartEndpoint, "application/json", bytes.NewBuffer([]byte(`{}`)))
	if err != nil {
		return fmt.Errorf("send restart command failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("restart command rejected: %d", resp.StatusCode)
	}
	log.Println("[RESTART] Restart command sent to Agent")
	notifier.Send("[RESTART] Restart command sent, waiting for services restart", nil)
	return nil
}

// waitForRestartFeedback 等待 Agent 反馈
func waitForRestartFeedback(cluster *config.ClusterConfig) error {
	client := &http.Client{Timeout: 10 * time.Second}
	deadline := time.Now().Add(restartTimeout)

	for time.Now().Before(deadline) {
		resp, err := client.Post(cluster.AgentEndpoint+restartFeedbackEndpoint, "application/json", bytes.NewBuffer([]byte(`{"status":"check"}`)))
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			var feedback struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(body, &feedback) == nil && feedback.Status == "success" {
				log.Println("[RESTART] Agent reported restart completed")
				return nil
			}
		}
		time.Sleep(30 * time.Second)
	}
	return fmt.Errorf("timeout waiting for agent restart feedback")
}

// calculateScaleDownCount 计算需要缩减多少节点（简化版）
func calculateScaleDownCount(totalNodes int, avgUtil float64) int {
	// 示例：如果利用率 < LowThreshold，缩减 1 个
	return 1
}