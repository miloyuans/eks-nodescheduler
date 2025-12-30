package autoscaler

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

type Config struct {
	ClusterName    string   `yaml:"clusterName"`
	HighThreshold  int      `yaml:"highThreshold"`
	LowThreshold   int      `yaml:"lowThreshold"`
	MaxThreshold   int      `yaml:"maxThreshold"`
	CooldownSeconds int     `yaml:"cooldownSeconds"`
	NodeGroups     []string `yaml:"nodeGroups"`
}

type Autoscaler struct {
	config     Config
	k8sClient  kubernetes.Interface
	eksClient  *eks.Client
	dryRun     bool
	lastScaleDown map[string]time.Time
}

func NewAutoscaler(config Config, k8sClient kubernetes.Interface, eksClient *eks.Client, dryRun bool) *Autoscaler {
	return &Autoscaler{
		config:        config,
		k8sClient:     k8sClient,
		eksClient:     eksClient,
		dryRun:        dryRun,
		lastScaleDown: make(map[string]time.Time),
	}
}

func (a *Autoscaler) RunOnce(ctx context.Context) {
	log.Printf("Running autoscaler cycle (dry-run: %v)", a.dryRun)

	nodegroups, err := a.getNodeGroups(ctx)
	if err != nil {
		log.Printf("Failed to get nodegroups: %v", err)
		return
	}

	for _, ng := range nodegroups {
		ngName := *ng.NodegroupName
		if len(a.config.NodeGroups) > 0 && !contains(a.config.NodeGroups, ngName) {
			continue
		}

		nodes, err := a.getNodesInGroup(ngName)
		if err != nil || len(nodes) <= int(*ng.ScalingConfig.MinSize) {
			continue
		}

		avgUtil, nodeUtils := a.calculateUtilization(nodes)
		lowNode := a.selectLowUtilNode(nodeUtils, avgUtil)

		if lowNode == nil || !a.isImbalanced(nodeUtils, avgUtil) {
			continue
		}

		if avgUtil > float64(a.config.HighThreshold)/100 {
			a.scaleGroup(ng, 1)
			continue
		}

		if avgUtil < float64(a.config.LowThreshold)/100 && *ng.ScalingConfig.DesiredSize > *ng.ScalingConfig.MinSize {
			if time.Since(a.lastScaleDown[ngName]) < time.Duration(a.config.CooldownSeconds)*time.Second {
				continue
			}

			if a.simulateRemoval(nodes, lowNode) {
				a.drainNode(lowNode)
				a.scaleGroup(ng, -1)
				a.lastScaleDown[ngName] = time.Now()
			}
		}
	}
}

func (a *Autoscaler) getNodeGroups(ctx context.Context) ([]*eks.Nodegroup, error) {
	input := &eks.ListNodegroupsInput{ClusterName: aws.String(a.config.ClusterName)}
	output, err := a.eksClient.ListNodegroups(ctx, input)
	if err != nil {
		return nil, err
	}

	var groups []*eks.Nodegroup
	for _, name := range output.Nodegroups {
		descInput := &eks.DescribeNodegroupInput{
			ClusterName:   aws.String(a.config.ClusterName),
			NodegroupName: name,
		}
		descOutput, err := a.eksClient.DescribeNodegroup(ctx, descInput)
		if err != nil {
			continue
		}
		groups = append(groups, descOutput.Nodegroup)
	}
	return groups, nil
}

func (a *Autoscaler) getNodesInGroup(ngName string) ([]*v1.Node, error) {
	selector := labels.SelectorFromSet(labels.Set{"eks.amazonaws.com/nodegroup": ngName})
	return a.k8sClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: selector.String()})
}

func (a *Autoscaler) calculateUtilization(nodes []*v1.Node) (float64, map[string]float64) {
	var total float64
	utils := make(map[string]float64)
	for _, n := range nodes {
		pods, _ := a.k8sClient.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{FieldSelector: "spec.nodeName=" + n.Name})
		util := 0.0
		alloc := n.Status.Allocatable.Cpu().MilliValue()
		if alloc > 0 {
			var req int64
			for _, pod := range pods.Items {
				for _, c := range pod.Spec.Containers {
					req += c.Resources.Requests.Cpu().MilliValue()
				}
			}
			util = float64(req) / float64(alloc)
		}
		utils[n.Name] = util
		total += util
	}
	avg := 0.0
	if len(nodes) > 0 {
		avg = total / float64(len(nodes))
	}
	return avg, utils
}

func (a *Autoscaler) isImbalanced(nodeUtils map[string]float64, avgUtil float64) bool {
	hasLow := false
	for _, u := range nodeUtils {
		if u > float64(a.config.MaxThreshold)/100 {
			return false
		}
		if u < avgUtil {
			hasLow = true
		}
	}
	return hasLow
}

func (a *Autoscaler) selectLowUtilNode(nodeUtils map[string]float64, avgUtil float64) *v1.Node {
	var candidate *v1.Node
	minUtil := 1.0
	for name, u := range nodeUtils {
		if u < avgUtil && u < minUtil {
			minUtil = u
			n, _ := a.k8sClient.CoreV1().Nodes().Get(context.TODO(), name, metav1.GetOptions{})
			candidate = n
		}
	}
	return candidate
}

func (a *Autoscaler) simulateRemoval(nodes []*v1.Node, remove *v1.Node) bool {
	totalReq := int64(0)
	for _, n := range nodes {
		pods, _ := a.k8sClient.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{FieldSelector: "spec.nodeName=" + n.Name})
		for _, pod := range pods.Items {
			for _, c := range pod.Spec.Containers {
				totalReq += c.Resources.Requests.Cpu().MilliValue()
			}
		}
	}

	remaining := len(nodes) - 1
	if remaining == 0 {
		return false
	}
	avgReq := totalReq / int64(remaining)

	for _, n := range nodes {
		if n.Name == remove.Name {
			continue
		}
		alloc := n.Status.Allocatable.Cpu().MilliValue()
		if float64(avgReq)/float64(alloc) > float64(a.config.MaxThreshold)/100 {
			return false
		}
	}
	return true
}

func (a *Autoscaler) drainNode(node *v1.Node) {
	if a.dryRun {
		log.Printf("Dry-run: Would drain node %s", node.Name)
		return
	}

	// Cordon
	node.Spec.Unschedulable = true
	a.k8sClient.CoreV1().Nodes().Update(context.TODO(), node, metav1.UpdateOptions{})

	// Evict pods
	pods, _ := a.k8sClient.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{FieldSelector: "spec.nodeName=" + node.Name})
	for _, pod := range pods.Items {
		eviction := &v1.Eviction{
			ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace},
		}
		a.k8sClient.CoreV1().Pods(pod.Namespace).Evict(context.TODO(), eviction)
	}

	// Wait for eviction
	time.Sleep(30 * time.Second)  // 简化等待，实际可轮询
	log.Printf("Drained node %s", node.Name)
}

func (a *Autoscaler) scaleGroup(ng *eks.Nodegroup, delta int64) {
	newDesired := *ng.ScalingConfig.DesiredSize + delta
	if newDesired < *ng.ScalingConfig.MinSize || newDesired > *ng.ScalingConfig.MaxSize {
		log.Printf("Scale out of bounds for %s", *ng.NodegroupName)
		return
	}

	if a.dryRun {
		log.Printf("Dry-run: Would scale %s to %d", *ng.NodegroupName, newDesired)
		return
	}

	input := &eks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String(a.config.ClusterName),
		NodegroupName: ng.NodegroupName,
		ScalingConfig: &eks.NodegroupScalingConfig{DesiredSize: aws.Int64(newDesired)},
	}
	a.eksClient.UpdateNodegroupConfig(context.TODO(), input)
	log.Printf("Scaled %s to %d", *ng.NodegroupName, newDesired)
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}