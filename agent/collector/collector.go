// agent/collector/collector.go
package collector

import (
	"context"
	"fmt"
	"log"

	"agent/model"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func Collect(clusterName string, filterNodeGroups []string) (model.ReportRequest, error) {
	// 使用 in-cluster config（Agent 运行在集群内）
	config, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		return model.ReportRequest{}, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return model.ReportRequest{}, fmt.Errorf("failed to create clientset: %w", err)
	}

	nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return model.ReportRequest{}, fmt.Errorf("failed to list nodes: %w", err)
	}

	// 按 nodegroup 分组
	nodeGroups := make(map[string]*model.NodeGroupData)

	for _, node := range nodes.Items {
		ngName := node.Labels["eks.amazonaws.com/nodegroup"]
		if ngName == "" {
			ngName = "unknown"
		}

		if len(filterNodeGroups) > 0 {
			found := false
			for _, filter := range filterNodeGroups {
				if filter == ngName {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		ng, ok := nodeGroups[ngName]
		if !ok {
			ng = &model.NodeGroupData{
				Name:      ngName,
				NodeUtils: make(map[string]float64),
				Nodes:     []model.NodeInfo{},
			}
			nodeGroups[ngName] = ng
		}

		// 获取该节点上 Pod 请求
		pods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
			FieldSelector: "spec.nodeName=" + node.Name,
		})
		if err != nil {
			log.Printf("[WARN] Failed to list pods on node %s: %v", node.Name, err)
			continue
		}

		var reqCpu int64
		for _, pod := range pods.Items {
			for _, container := range pod.Spec.Containers {
				reqCpu += container.Resources.Requests.Cpu().MilliValue()
			}
		}

		allocCpu := node.Status.Allocatable.Cpu().MilliValue()
		util := 0.0
		if allocCpu > 0 {
			util = float64(reqCpu) / float64(allocCpu)
		}

		ng.NodeUtils[node.Name] = util

		ng.Nodes = append(ng.Nodes, model.NodeInfo{
			Name:                node.Name,
			InstanceId:          getInstanceID(&node),
			RequestCpuMilli:     reqCpu,
			AllocatableCpuMilli: int64(allocCpu),
		})
	}

	// 转换为上报结构
	var ngList []model.NodeGroupData
	for _, ng := range nodeGroups {
		ngList = append(ngList, *ng)
	}

	return model.ReportRequest{
		ClusterName: clusterName,
		NodeGroups:  ngList,
		Timestamp:   time.Now().Unix(),
	}, nil
}

func getInstanceID(node *v1.Node) string {
	if node.Spec.ProviderID == "" {
		return ""
	}
	parts := strings.Split(node.Spec.ProviderID, "/")
	return parts[len(parts)-1]
}