// autoscaler/autoscaler.go
package autoscaler

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
	"k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

type Config struct {
	ClusterName     string   `yaml:"clusterName"`
	HighThreshold   int      `yaml:"highThreshold"`
	LowThreshold    int      `yaml:"lowThreshold"`
	MaxThreshold    int      `yaml:"maxThreshold"`
	CooldownSeconds int      `yaml:"cooldownSeconds"`
	NodeGroups      []string `yaml:"nodeGroups"`
}

type Autoscaler struct {
	config        Config
	k8sClient     kubernetes.Interface
	eksClient     *eks.Client
	ec2Client     *ec2.Client
	asgClient     *autoscaling.Client
	dryRun        bool
	lastScaleDown map[string]time.Time
}

func NewAutoscaler(
	config Config,
	k8sClient kubernetes.Interface,
	eksClient *eks.Client,
	ec2Client *ec2.Client,
	asgClient *autoscaling.Client,
	dryRun bool,
) *Autoscaler {
	return &Autoscaler{
		config:        config,
		k8sClient:     k8sClient,
		eksClient:     eksClient,
		ec2Client:     ec2Client,
		asgClient:     asgClient,
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
		if ng == nil || ng.NodegroupName == nil {
			continue
		}
		ngName := aws.StringValue(ng.NodegroupName)

		if len(a.config.NodeGroups) > 0 && !contains(a.config.NodeGroups, ngName) {
			continue
		}

		// 获取关联的 ASG 名称（安全方式）
		asgName := ""
		if ng.Resources != nil && len(ng.Resources.AutoScalingGroups) > 0 {
			firstASG := ng.Resources.AutoScalingGroups[0]
			if firstASG != nil && firstASG.Name != nil {
				asgName = aws.StringValue(firstASG.Name)
			}
		}
		if asgName == "" {
			log.Printf("No ASG found for nodegroup %s", ngName)
			continue
		}

		nodes, err := a.getNodesInGroup(ctx, ngName)
		if err != nil {
			log.Printf("Failed to get nodes for %s: %v", ngName, err)
			continue
		}

		if ng.ScalingConfig == nil ||
			ng.ScalingConfig.MinSize == nil ||
			len(nodes) <= int(aws.Int32Value(ng.ScalingConfig.MinSize)) {
			continue
		}

		avgUtil, nodeUtils := a.calculateUtilization(ctx, nodes)
		lowNode := a.selectLowUtilNode(ctx, nodeUtils, avgUtil)

		if lowNode == nil || !a.isImbalanced(nodeUtils, avgUtil) {
			continue
		}

		// Scale up
		if avgUtil > float64(a.config.HighThreshold)/100 {
			a.scaleUp(ctx, ng)
			continue
		}

		// Scale down
		if avgUtil < float64(a.config.LowThreshold)/100 &&
			ng.ScalingConfig.DesiredSize != nil &&
			ng.ScalingConfig.MinSize != nil &&
			aws.Int32Value(ng.ScalingConfig.DesiredSize) > aws.Int32Value(ng.ScalingConfig.MinSize) {

			if time.Since(a.lastScaleDown[ngName]) < time.Duration(a.config.CooldownSeconds)*time.Second {
				continue
			}

			if a.simulateRemoval(nodes, lowNode) {
				a.drainNode(ctx, lowNode)
				instanceID := a.getInstanceID(lowNode)
				if instanceID != "" {
					a.terminateSpecificInstance(ctx, asgName, instanceID)
					a.lastScaleDown[ngName] = time.Now()
				}
			}
		}
	}
}

func (a *Autoscaler) getNodeGroups(ctx context.Context) ([]*types.Nodegroup, error) {
	input := &eks.ListNodegroupsInput{
		ClusterName: aws.String(a.config.ClusterName),
	}
	output, err := a.eksClient.ListNodegroups(ctx, input)
	if err != nil {
		return nil, err
	}

	var groups []*types.Nodegroup
	for _, name := range output.Nodegroups {
		descInput := &eks.DescribeNodegroupInput{
			ClusterName:   aws.String(a.config.ClusterName),
			NodegroupName: aws.String(name),
		}
		descOutput, err := a.eksClient.DescribeNodegroup(ctx, descInput)
		if err != nil {
			log.Printf("Failed to describe nodegroup %s: %v", name, err)
			continue
		}
		if descOutput.Nodegroup != nil {
			groups = append(groups, descOutput.Nodegroup)
		}
	}
	return groups, nil
}

func (a *Autoscaler) getNodesInGroup(ctx context.Context, ngName string) ([]*v1.Node, error) {
	selector := labels.SelectorFromSet(labels.Set{"eks.amazonaws.com/nodegroup": ngName})
	list, err := a.k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return nil, err
	}
	nodes := make([]*v1.Node, 0, len(list.Items))
	for i := range list.Items {
		nodes = append(nodes, &list.Items[i])
	}
	return nodes, nil
}

func (a *Autoscaler) calculateUtilization(ctx context.Context, nodes []*v1.Node) (float64, map[string]float64) {
	var total float64
	utils := make(map[string]float64)

	for _, n := range nodes {
		pods, _ := a.k8sClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{
			FieldSelector: "spec.nodeName=" + n.Name,
		})

		alloc := n.Status.Allocatable.Cpu().MilliValue()
		if alloc == 0 {
			utils[n.Name] = 0.0
			continue
		}

		var req int64
		for _, pod := range pods.Items {
			for _, c := range pod.Spec.Containers {
				req += c.Resources.Requests.Cpu().MilliValue()
			}
		}

		util := float64(req) / float64(alloc)
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

func (a *Autoscaler) selectLowUtilNode(ctx context.Context, nodeUtils map[string]float64, avgUtil float64) *v1.Node {
	var candidate *v1.Node
	minUtil := 1.0

	for name, u := range nodeUtils {
		if u < avgUtil && u < minUtil {
			minUtil = u
			node, err := a.k8sClient.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				continue
			}
			candidate = node
		}
	}
	return candidate
}

func (a *Autoscaler) simulateRemoval(nodes []*v1.Node, remove *v1.Node) bool {
	var totalReq int64
	for _, n := range nodes {
		pods, _ := a.k8sClient.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{
			FieldSelector: "spec.nodeName=" + n.Name,
		})
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
	avgReqPerNode := totalReq / int64(remaining)

	for _, n := range nodes {
		if n.Name == remove.Name {
			continue
		}
		alloc := n.Status.Allocatable.Cpu().MilliValue()
		if alloc > 0 && float64(avgReqPerNode)/float64(alloc) > float64(a.config.MaxThreshold)/100 {
			return false
		}
	}
	return true
}

func (a *Autoscaler) drainNode(ctx context.Context, node *v1.Node) {
	if a.dryRun {
		log.Printf("Dry-run: Would drain node %s", node.Name)
		return
	}

	// Cordon
	nodeCopy := node.DeepCopy()
	nodeCopy.Spec.Unschedulable = true
	_, err := a.k8sClient.CoreV1().Nodes().Update(ctx, nodeCopy, metav1.UpdateOptions{})
	if err != nil {
		log.Printf("Failed to cordon node %s: %v", node.Name, err)
		return
	}

	// Evict pods
	pods, err := a.k8sClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + node.Name,
	})
	if err != nil {
		log.Printf("Failed to list pods on node %s: %v", node.Name, err)
		return
	}

	for _, pod := range pods.Items {
		if pod.Namespace == "kube-system" {
			continue
		}
		eviction := &policyv1beta1.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			},
		}
		err := a.k8sClient.PolicyV1beta1().Evictions(pod.Namespace).Evict(ctx, eviction)
		if err != nil {
			log.Printf("Failed to evict pod %s/%s: %v", pod.Namespace, pod.Name, err)
		}
	}

	// 轮询等待
	timeout := time.NewTimer(5 * time.Minute)
	ticker := time.NewTicker(10 * time.Second)
	defer timeout.Stop()
	defer ticker.Stop()

	for {
		select {
		case <-timeout.C:
			log.Printf("Timeout draining node %s", node.Name)
			return
		case <-ticker.C:
			current, _ := a.k8sClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{
				FieldSelector: "spec.nodeName=" + node.Name,
			})
			nonDaemon := 0
			for _, p := range current.Items {
				isDaemon := false
				for _, ref := range p.OwnerReferences {
					if ref.Kind == "DaemonSet" {
						isDaemon = true
						break
					}
				}
				if !isDaemon {
					nonDaemon++
				}
			}
			if nonDaemon == 0 {
				log.Printf("Successfully drained node %s", node.Name)
				return
			}
		}
	}
}

func (a *Autoscaler) getInstanceID(node *v1.Node) string {
	if node.Spec.ProviderID == "" {
		return ""
	}
	parts := strings.Split(node.Spec.ProviderID, "/")
	return parts[len(parts)-1]
}

func (a *Autoscaler) terminateSpecificInstance(ctx context.Context, asgName, instanceID string) {
	if a.dryRun {
		log.Printf("Dry-run: Would terminate instance %s in ASG %s", instanceID, asgName)
		return
	}

	input := &autoscaling.TerminateInstanceInAutoScalingGroupInput{
		InstanceId:                     aws.String(instanceID),
		ShouldDecrementDesiredCapacity: aws.Bool(true),
	}
	_, err := a.asgClient.TerminateInstanceInAutoScalingGroup(ctx, input)
	if err != nil {
		log.Printf("Failed to terminate instance %s: %v", instanceID, err)
		return
	}
	log.Printf("Terminated instance %s in ASG %s", instanceID, asgName)
}

func (a *Autoscaler) scaleUp(ctx context.Context, ng *types.Nodegroup) {
	if ng.ScalingConfig == nil ||
		ng.ScalingConfig.DesiredSize == nil ||
		ng.ScalingConfig.MaxSize == nil {
		log.Printf("Scaling config missing for nodegroup %s", aws.StringValue(ng.NodegroupName))
		return
	}

	currentDesired := aws.Int32Value(ng.ScalingConfig.DesiredSize)
	newDesired := currentDesired + 1

	if newDesired > aws.Int32Value(ng.ScalingConfig.MaxSize) {
		log.Printf("Scale up would exceed MaxSize for %s", aws.StringValue(ng.NodegroupName))
		return
	}

	if a.dryRun {
		log.Printf("Dry-run: Would scale up %s to %d", aws.StringValue(ng.NodegroupName), newDesired)
		return
	}

	input := &eks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String(a.config.ClusterName),
		NodegroupName: ng.NodegroupName,
		ScalingConfig: &types.NodegroupScalingConfig{
			MinSize:     ng.ScalingConfig.MinSize,
			MaxSize:     ng.ScalingConfig.MaxSize,
			DesiredSize: aws.Int32(newDesired),
		},
	}

	_, err := a.eksClient.UpdateNodegroupConfig(ctx, input)
	if err != nil {
		log.Printf("Failed to scale up %s: %v", aws.StringValue(ng.NodegroupName), err)
		return
	}
	log.Printf("Scaled up %s to %d", aws.StringValue(ng.NodegroupName), newDesired)
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}