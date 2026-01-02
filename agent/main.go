// agent/main.go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"sync"
	"time"

	"agent/collector"
	"agent/reporter"

	"gopkg.in/yaml.v3"
)

// AgentConfig 配置结构体
type AgentConfig struct {
	CentralEndpoint       string   `yaml:"central_endpoint"`
	ReportIntervalSeconds int      `yaml:"report_interval_seconds"`
	ClusterName           string   `yaml:"cluster_name"`
	NodeGroups            []string `yaml:"node_groups"`
}

// LoadConfig 从 YAML 文件加载配置
func LoadConfig(file string) (*AgentConfig, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	var cfg AgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// 设置默认值
	if cfg.ReportIntervalSeconds <= 0 {
		cfg.ReportIntervalSeconds = 300 // 默认 5 分钟
	}

	if cfg.CentralEndpoint == "" {
		return nil, fmt.Errorf("central_endpoint is required")
	}

	if cfg.ClusterName == "" {
		return nil, fmt.Errorf("cluster_name is required")
	}

	return &cfg, nil
}

func main() {
	// 使用所有 CPU 核
	runtime.GOMAXPROCS(runtime.NumCPU())

	configFile := "config.yaml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	// 加载配置
	cfg, err := LoadConfig(configFile)
	if err != nil {
		log.Fatalf("[FATAL] Failed to load config %s: %v", configFile, err)
	}

	log.Printf("[INFO] Agent starting for cluster: %s", cfg.ClusterName)
	log.Printf("[INFO] Central endpoint: %s", cfg.CentralEndpoint)
	log.Printf("[INFO] Report interval: %d seconds", cfg.ReportIntervalSeconds)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// 启动定期上报任务
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
					log.Printf("[ERROR] Data collection failed: %v", err)
					continue
				}

				if err := reporter.Report(cfg.CentralEndpoint, report); err != nil {
					log.Printf("[ERROR] Report to central failed: %v", err)
				} else {
					log.Printf("[INFO] Report successfully sent (%d nodegroups)", len(report.NodeGroups))
				}
			}
		}
	}()

	// 优雅关闭
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("[INFO] Shutdown signal received, shutting down gracefully...")

	cancel()
	wg.Wait()

	log.Println("[INFO] Agent shutdown complete")
}