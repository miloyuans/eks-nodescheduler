// agent/main.go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"agent/collector"
	"agent/reporter"
)

func main() {
	configFile := "config.yaml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	cfg, err := LoadConfig(configFile)
	if err != nil {
		log.Fatalf("[FATAL] Failed to load config %s: %v", configFile, err)
	}

	log.Printf("[INFO] Agent starting for cluster %s", cfg.ClusterName)
	log.Printf("[INFO] Report endpoint: %s", cfg.CentralEndpoint)
	log.Printf("[INFO] Report interval: %d seconds", cfg.ReportIntervalSeconds)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// 启动定期上报
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Duration(cfg.ReportIntervalSeconds) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Println("[INFO] Reporter shutting down")
				return
			case <-ticker.C:
				report, err := collector.Collect(cfg.ClusterName, cfg.NodeGroups)
				if err != nil {
					log.Printf("[ERROR] Collect data failed: %v", err)
					continue
				}

				if err := reporter.Report(cfg.CentralEndpoint, report); err != nil {
					log.Printf("[ERROR] Report failed: %v", err)
				} else {
					log.Printf("[INFO] Report sent successfully (%d nodegroups)", len(report.NodeGroups))
				}
			}
		}
	}()

	// 优雅关闭
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("[INFO] Shutdown signal received")

	cancel()
	wg.Wait()

	log.Println("[INFO] Agent shutdown complete")
}