// central/processor/cluster_processor.go
package processor

import (
	"context"
	"crypto/md5"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"central/config"
	"central/core"
	"central/model"
	"central/notifier"
	"central/storage"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
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

	// 启动每日 nodegroup 管理
	go manageDailyNodeGroups(ctx, eksClient, cluster)

	lastScaleDown := make(map[string]time.Time)

	ch := central.GetClusterChan(cluster.Name)
	for {
		select {
		case req := <-ch:
			log.Printf("[INFO] Received report from cluster %s with %d nodegroups", req.ClusterName, len(req.NodeGroups))

			// 存储源数据
			if err := storage.StoreRawReport(req.ClusterName, req); err != nil {
				log.Printf("[ERROR] Failed to store raw report for %s: %v", req.ClusterName, err)
				continue
			}
			log.Printf("[INFO] Raw report stored for %s", req.ClusterName)

			// 计算指标
			totalNodes, totalRequestCpu, totalAllocatableCpu := calculateClusterLoad(req.NodeGroups)

			avgUtil := 0.0
			if totalAllocatableCpu > 0 {
				avgUtil = float64(totalRequestCpu) / float64(totalAllocatableCpu)
			}

			// 生成事件 ID 用于去重
			eventID := generateEventID(req.ClusterName, req.Timestamp, avgUtil, totalNodes)

			actionTaken := false
			eventType := ""
			detail := ""

			tomorrowNG := getNodeGroupName(cluster, time.Now().Add(24*time.Hour))

			if avgUtil > float64(cluster.HighThreshold)/100 {
				addCount := calculateScaleUpCount(avgUtil, totalNodes, cluster.UtilThreshold)
				if addCount > 0 {
					scaleNodeGroup(eksClient, cluster.Name, tomorrowNG, int32(addCount))
					actionTaken = true
					eventType = "scale_up"
					detail = fmt.Sprintf("Added %d nodes to tomorrow group %s", addCount, tomorrowNG)
					notifier.Send(fmt.Sprintf("[↑] Cluster %s scaled up by %d nodes (tomorrow group %s)", cluster.Name, addCount, tomorrowNG), central.GetTelegramChatIDs())
				}
			} else if avgUtil < float64(cluster.LowThreshold)/100 {
				if time.Since(lastScaleDown["cluster"]) < time.Duration(cluster.CooldownSeconds)*time.Second {
					log.Printf("[INFO] Scale down cooldown active for %s", cluster.Name)
					continue
				}

				reduceCount := calculateScaleDownCount(totalNodes, avgUtil)
				if reduceCount > 0 {
					if err := performScaleDown(ctx, eksClient, asgClient, cluster, req, tomorrowNG, reduceCount); err != nil {
						log.Printf("[ERROR] Scale down failed for %s: %v", cluster.Name, err)
						notifier.Send(fmt.Sprintf("[FAILED] Scale down failed for %s: %v", cluster.Name, err), central.GetTelegramChatIDs())
					} else {
						actionTaken = true
						eventType = "scale_down"
						detail = fmt.Sprintf("Reduced %d nodes", reduceCount)
						lastScaleDown["cluster"] = time.Now()
						notifier.Send(fmt.Sprintf("[↓] Cluster %s scaled down by %d nodes", cluster.Name, reduceCount), central.GetTelegramChatIDs())
					}
				}
			}

			// 如果有操作，存储事件（去重）
			if actionTaken {
				if storage.EventExists(req.ClusterName, eventID) {
					log.Printf("[INFO] Duplicate event skipped for %s (ID: %s)", req.ClusterName, eventID)
				} else {
					event := model.EventRecord{
						ClusterName: req.ClusterName,
						Timestamp:   req.Timestamp,
						Type:        eventType,
						Detail:      detail,
						EventID:     eventID,
					}
					if err := storage.StoreEvent(req.ClusterName, event); err != nil {
						log.Printf("[ERROR] Failed to store event for %s: %v", req.ClusterName, err)
					} else {
						log.Printf("[INFO] Event stored for %s (ID: %s)", req.ClusterName, eventID)
					}
				}
			}

		case <-ctx.Done():
			log.Printf("[INFO] Processor for cluster %s shutting down", cluster.Name)
			return
		}
	}
}

// generateEventID 生成唯一事件 ID（用于去重）
func generateEventID(clusterName string, timestamp int64, avgUtil float64, totalNodes int) string {
	data := fmt.Sprintf("%s-%d-%.4f-%d", clusterName, timestamp, avgUtil, totalNodes)
	hash := md5.Sum([]byte(data))
	return fmt.Sprintf("%x", hash)
}

// getNodeGroupName 生成 nodegroup 名称：prefix + YYYYMMDD
func getNodeGroupName(cluster *config.ClusterConfig, t time.Time) string {
	return cluster.NodeGroupPrefix + t.Format("20060102")
}

// manageDailyNodeGroups 每天创建今天和明天空 nodegroup，并清理历史空组
func manageDailyNodeGroups(ctx context.Context, client *eks.Client, cluster *config.ClusterConfig) {
	ticker := time.NewTicker(6 * time.Hour)
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
		return
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
		AmiType:       types.AMITypes(cluster.AmiType),
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
	for i := 2; i <= 7; i++ {
		oldDate := time.Now().AddDate(0, 0, -i)
		oldNG := getNodeGroupName(cluster, oldDate)

		desc, err := client.DescribeNodegroup(context.Background(), &eks.DescribeNodegroupInput{
			ClusterName:   aws.String(cluster.Name),
			NodegroupName: aws.String(oldNG),
		})
		if err != nil {
			continue
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

// calculateClusterLoad 计算集群总节点数、总请求和总可分配 CPU
func calculateClusterLoad(nodeGroups []model.NodeGroupData) (totalNodes int, totalRequest, totalAllocatable int64) {
	for _, ng := range nodeGroups {
		totalNodes += len(ng.Nodes)
		for _, node := range ng.Nodes {
			totalRequest += node.RequestCpuMilli
			totalAllocatable += node.AllocatableCpuMilli
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
		if add > 10 {
			return add
		}
	}
}

// calculateScaleDownCount 计算需要缩减多少节点（简化）
func calculateScaleDownCount(totalNodes int, avgUtil float64) int {
	return 1
}

// scaleNodeGroup 调整 nodegroup DesiredSize
func scaleNodeGroup(client *eks.Client, clusterName, ngName string, desiredSize int32) {
	input := &eks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String(clusterName),
		NodegroupName: aws.String(ngName),
		ScalingConfig: &types.NodegroupScalingConfig{
			DesiredSize: aws.Int32(desiredSize),
		},
	}
	_, _ = client.UpdateNodegroupConfig(context.Background(), input)
}

// performScaleDown 执行缩容流程
func performScaleDown(ctx context.Context, eksClient *eks.Client, asgClient *autoscaling.Client, cluster *config.ClusterConfig, req model.ReportRequest, tomorrowNG string, reduceCount int) error {
	scaleNodeGroup(eksClient, cluster.Name, tomorrowNG, int32(reduceCount))

	// Cordon 旧节点（模拟）
	for _, ng := range req.NodeGroups {
		if ng.Name == tomorrowNG {
			continue
		}
		for _, node := range ng.Nodes {
			log.Printf("[CORDON] Cordon node %s in old group %s", node.Name, ng.Name)
		}
	}

	// 这里可以扩展为 Telegram 下发重启指令

	notifier.Send(fmt.Sprintf("[↓] Cluster %s scaled down by %d nodes", cluster.Name, reduceCount), nil)
	return nil
}