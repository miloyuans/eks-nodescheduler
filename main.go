// main.go
package main

import (
	"context"
	"flag"
	"log"

	"eks-nodescheduler/autoscaler"
	"eks-nodescheduler/awsclients"
	"eks-nodescheduler/config"
	"eks-nodescheduler/k8s"
)

func main() {
	configFile := flag.String("config", "config.yaml", "Path to config file")
	dryRun := flag.Bool("dry-run", true, "Enable dry-run mode")
	flag.Parse()

	cfg, err := config.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	k8sClient, err := k8s.NewK8sClient()
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	eksClient, err := awsclients.NewEKSClient()
	if err != nil {
		log.Fatalf("Failed to create EKS client: %v", err)
	}

	ec2Client, err := awsclients.NewEC2Client()
	if err != nil {
		log.Fatalf("Failed to create EC2 client: %v", err)
	}

	asgClient, err := awsclients.NewASGClient()
	if err != nil {
		log.Fatalf("Failed to create AutoScaling client: %v", err)
	}

	as := autoscaler.NewAutoscaler(cfg, k8sClient, eksClient, ec2Client, asgClient, *dryRun)
	as.RunOnce(context.Background())
}