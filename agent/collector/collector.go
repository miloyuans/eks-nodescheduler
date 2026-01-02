// agent/collector/collector.go
package collector

import (
	"context"
	"fmt"
	"log"
	"time"

	"agent/model"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var reportChan chan model.ReportRequest

// InitCollector 初始化事件监听器（Node 变化触发上报）
func InitCollector(ctx context.Context, clusterName string, filterNodeGroups []string, ch chan model.ReportRequest) error {
	reportChan = ch

	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("in-cluster config failed: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create clientset failed: %w", err)
	}

	factory := informers.NewSharedInformerFactory(clientset, 10*time.Minute)
	nodeInformer := factory.Core().V1().Nodes().Informer()
	nodeInformer.AddEventHandler(&nodeEventHandler{
		clusterName:      clusterName,
		filterNodeGroups: filterNodeGroups,
	})

	stopCh := make(chan struct{})
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)

	log.Println("[COLLECT] Node informer started")

	// 阻塞直到 context 取消
	<-ctx.Done()
	close(stopCh)
	return nil
}

// CollectFull 启动时全量采集一次
func CollectFull(clusterName string, filterNodeGroups []string) (model.ReportRequest, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return model.ReportRequest{}, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return model.ReportRequest{}, err
	}

	nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return model.ReportRequest{}, err
	}

	log.Printf("[COLLECT] Full collection: %d nodes fetched", len(nodes.Items))

	nodeGroups := make(map[string]*model.NodeGroupData)

	for _, node := range nodes.Items {
		ngName := node.Labels["eks.amazonaws.com/nodegroup"]
		if ngName == "" {
			ngName = "unknown"
		}

		if len(filterNodeGroups) > 0 {
			found := false
			for _, f := range filterNodeGroups {
				if f == ngName {
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

		allocCpu := node.Status.Allocatable.Cpu().MilliValue()
		util := 0.0 // 简化：这里可以用其他方式计算，这里用 0 占位
		ng.NodeUtils[node.Name] = util

		ng.Nodes = append(ng.Nodes, model.NodeInfo{
			Name:                node.Name,
			InstanceId:          getInstanceID(&node),
			RequestCpuMilli:     0,
			AllocatableCpuMilli: int64(allocCpu),
		})
	}

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

type nodeEventHandler struct {
	clusterName      string
	filterNodeGroups []string
}

func (h *nodeEventHandler) OnAdd(obj interface{}) {
	h.triggerReport()
}

func (h *nodeEventHandler) OnUpdate(old, new interface{}) {
	h.triggerReport()
}

func (h *nodeEventHandler) OnDelete(obj interface{}) {
	h.triggerReport()
}

func (h *nodeEventHandler) triggerReport() {
	log.Println("[EVENT] Node event detected, triggering report")
	go func() {
		report, err := CollectFull(h.clusterName, h.filterNodeGroups)
		if err != nil {
			log.Printf("[ERROR] Event-triggered collection failed: %v", err)
			return
		}
		reportChan <- report
	}()
}

func getInstanceID(node *v1.Node) string {
	if node.Spec.ProviderID == "" {
		return ""
	}
	parts := strings.Split(node.Spec.ProviderID, "/")
	return parts[len(parts)-1]
}