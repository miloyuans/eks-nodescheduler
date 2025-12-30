// autoscaler/autoscaler.go
package autoscaler

import (
	"context"
	"log"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/eks"
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

func NewAutoscaler(config Config, k8sClient kubernetes.Interface, eksClient *eks.Client, ec2Client *ec2.Client, asgClient *autoscaling.Client, dryRun bool) *Autoscaler {
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
		ngName := *ng.NodegroupName
		if len(a.config.NodeGroups) > 0 && !contains(a.config.NodeGroups, ngName) {
			continue
		}

		asgName := ""
		if len(ng.Resources.AutoScalingGroups) > 0 {
			asgName = *ng.Resources.AutoScalingGroups[0].Name
		} else {
			log.Printf("No ASG found for nodegroup %s", ngName)
			continue
		}

		nodes, err := a.getNodesInGroup(ctx, ngName)
		if err != nil {
			log.Printf("Failed to get nodes for %s: %v", ngName, err)
			continue
		}
		if len(nodes) <= int(*ng.ScalingConfig.MinSize) {
			continue
		}

		avgUtil, nodeUtils := a.calculateUtilization(ctx, nodes)
		lowNode := a.selectLowUtilNode(ctx, nodeUtils, avgUtil)

		if lowNode == nil || !a.isImbalanced(nodeUtils, avgUtil) {
			continue
		}

		if avgUtil > float64(a.config.HighThreshold)/100 {
			a.scaleUp(ctx, ng)
			continue
		}

		if avgUtil < float64(a.config.LowThreshold)/100 && *ng.ScalingConfig.DesiredSize > *ng.ScalingConfig.MinSize {
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
			NodegroupName: aws.String(name),
		}
		descOutput, err := a.eksClient.DescribeNodegroup(ctx, descInput)
		if err != nil {
			log.Printf("Failed to describe nodegroup %s: %v", name, err)
			continue
		}
		groups = append(groups, descOutput.Nodegroup)
	}
	return groups, nil
}

func (a *Autoscaler) getNodesInGroup(ctx context.Context, ngName string) ([]*v1.Node, error) {
	selector := labels.SelectorFromSet(labels.Set{"eks.amazonaws.com/nodegroup": ngName})
	nodesList, err := a.k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, err
	}
	var nodes []*v1.Node
	for i := range nodesList.Items {
		nodes = append(nodes, &nodesList.Items[i])
	}
	return nodes, nil
}

func (a *Autoscaler) calculateUtilization(ctx context.Context, nodes []*v1.Node) (float64, map[string]float64) {
	var total float64
	utils := make(map[string]float64)
	for _, n := range nodes {
		pods, _ := a.k8sClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + n.Name})
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

func (a *Autoscaler) selectLowUtilNode(ctx context.Context, nodeUtils map[string]float64, avgUtil float64) *v1.Node {
	var candidate *v1.Node
	minUtil := 1.0
	for name, u := range nodeUtils {
		if u < avgUtil && u < minUtil {
			minUtil = u
			n, err := a.k8sClient.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				continue
			}
			candidate = n
		}
	}
	return candidate
}

func (a *Autoscaler) simulateRemoval(nodes []*v1.Node, remove *v1.Node) bool {
	totalReq := int64(0)
	for _, n := range nodes {
		alloc := n.Status.Allocatable.Cpu().MilliValue()
		util := 0.0 // We can recompute, but optimize by passing from earlier
		pods, _ := a.k8sClient.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{FieldSelector: "spec.nodeName=" + n.Name})
		var req int64
		for _, pod := range pods.Items {
			for _, c := range pod.Spec.Containers {
				req += c.Resources.Requests.Cpu().MilliValue()
			}
		}
		totalReq += req
		util = float64(req) / float64(alloc) // Not needed here
		_ = util
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

func (a *Autoscaler) drainNode(ctx context.Context, node *v1.Node) {
	if a.dryRun {
		log.Printf("Dry-run: Would drain node %s", node.Name)
		return
	}

	// Cordon the node
	nodeCopy := node.DeepCopy()
	nodeCopy.Spec.Unschedulable = true
	_, err := a.k8sClient.CoreV1().Nodes().Update(ctx, nodeCopy, metav1.UpdateOptions{})
	if err != nil {
		log.Printf("Failed to cordon node %s: %v", node.Name, err)
		return
	}

	// Evict pods
	pods, err := a.k8sClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + node.Name})
	if err != nil {
		log.Printf("Failed to list pods on node %s: %v", node.Name, err)
		return
	}
	for _, pod := range pods.Items {
		if pod.Namespace == "" || strings.HasPrefix(pod.Name, "kube-") { // Skip system pods if needed
			continue
		}
		eviction := &policyv1beta1.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			},
		}
		err := a.k8sClient.CoreV1().Pods(pod.Namespace).Evict(ctx, eviction)
		if err != nil {
			log.Printf("Failed to evict pod %s/%s: %v", pod.Namespace, pod.Name, err)
		}
	}

	// Poll until no non-DaemonSet pods remain
	timeout := time.After(5 * time.Minute)
	tick := time.Tick(10 * time.Second)
	for {
		select {
		case <-timeout:
			log.Printf("Timeout waiting for node %s to drain", node.Name)
			return
		case <-tick:
			pods, err := a.k8sClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + node.Name})
			if err != nil {
				continue
			}
			nonDS := 0
			for _, p := range pods.Items {
				isDS := false
				for _, owner := range p.OwnerReferences {
					if owner.Kind == "DaemonSet" {
						isDS = true
						break
					}
				}
				if !isDS {
					nonDS++
				}
			}
			if nonDS == 0 {
				log.Printf("Drained node %s", node.Name)
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
		log.Printf("Dry-run: Would terminate instance %s in ASG %s and decrement desired", instanceID, asgName)
		return
	}

	input := &autoscaling.TerminateInstanceInAutoScalingGroupInput{
		InstanceId:                     aws.String(instanceID),
		ShouldDecrementDesiredCapacity: aws.Bool(true),
	}
	_, err := a.asgClient.TerminateInstanceInAutoScalingGroup(ctx, input)
	if err != nil {
		log.Printf("Failed to terminate instance %s in ASG %s: %v", instanceID, asgName, err)
		return
	}
	log.Printf("Terminated instance %s in ASG %s and decremented desired", instanceID, asgName)
}

func (a *Autoscaler) scaleUp(ctx context.Context, ng *eks.Nodegroup) {
	newDesired := *ng.ScalingConfig.DesiredSize + 1
	if newDesired > *ng.ScalingConfig.MaxSize {
		log.Printf("Scale up out of bounds for %s", *ng.NodegroupName)
		return
	}

	if a.dryRun {
		log.Printf("Dry-run: Would scale up %s to %d", *ng.NodegroupName, newDesired)
		return
	}

	input := &eks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String(a.config.ClusterName),
		NodegroupName: ng.NodegroupName,
		ScalingConfig: &eks.NodegroupScalingConfig{
			DesiredSize: aws.Int64(newDesired),
		},
	}
	_, err := a.eksClient.UpdateNodegroupConfig(ctx, input)
	if err != nil {
		log.Printf("Failed to scale up %s: %v", *ng.NodegroupName, err)
		return
	}
	log.Printf("Scaled up %s to %d", *ng.NodegroupName, newDesired)
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}