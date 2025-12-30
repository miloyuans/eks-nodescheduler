// main.go
package main

import (
	"context"
	"flag"
	"log"

	"autoscaler/aws"
	"autoscaler/autoscaler"
	"autoscaler/config"
	"autoscaler/k8s"
)

func main() {
	configFile := flag.String("config", "config.yaml", "config file")
	dryRun := flag.Bool("dry-run", true, "dry run mode")
	flag.Parse()

	cfg, err := config.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	k8sClient, err := k8s.NewK8sClient()
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	eksClient, err := aws.NewEKSClient()
	if err != nil {
		log.Fatalf("Failed to create EKS client: %v", err)
	}

	ec2Client, err := aws.NewEC2Client()
	if err != nil {
		log.Fatalf("Failed to create EC2 client: %v", err)
	}

	asgClient, err := aws.NewASGClient()
	if err != nil {
		log.Fatalf("Failed to create ASG client: %v", err)
	}

	as := autoscaler.NewAutoscaler(cfg, k8sClient, eksClient, ec2Client, asgClient, *dryRun)
	as.RunOnce(context.Background())
}