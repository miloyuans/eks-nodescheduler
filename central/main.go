// central/main.go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"sync"

	"central/config"
	"central/core"
	"central/notifier"
	"central/processor"
	"central/server"
	"central/storage"
)

func main() {
	// 配置并发
	runtime.GOMAXPROCS(runtime.NumCPU())

	configFile := "config.yaml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	cfg, err := config.Load(configFile)
	if err != nil {
		log.Fatalf("Failed to load config %s: %v", configFile, err)
	}

	// 配置检查
	if err := validateConfig(cfg); err != nil {
		log.Fatalf("Config validation failed: %v", err)
	}

	// 初始化 Telegram 通知
	if err := notifier.Init(cfg); err != nil {
		log.Printf("Telegram init failed: %v (notifications disabled)", err)
	}

	// 初始化 MongoDB 并检查权限
	if err := storage.InitMongo(cfg); err != nil {
		log.Fatalf("MongoDB init failed: %v", err)
	}

	central := core.New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	if cfg.Server.HTTP.Enabled {
		wg.Add(1)
		go server.StartHTTP(ctx, &wg, cfg, central)
	}

	for i := range cfg.Accounts {
		acct := &cfg.Accounts[i]
		for j := range acct.Clusters {
			cluster := &acct.Clusters[j]
			wg.Add(1)
			go processor.ProcessCluster(ctx, &wg, central, *acct, cluster)
		}
	}

	log.Println("Central server started successfully")

	// 优雅关闭
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("Shutdown signal received, starting graceful shutdown...")

	cancel()
	wg.Wait()

	// 关闭 Mongo 连接
	storage.Shutdown()

	log.Println("Shutdown complete")
}

// validateConfig 检查配置有效性
func validateConfig(cfg *config.GlobalConfig) error {
	if cfg.Mongo.URI == "" {
		return fmt.Errorf("mongo URI is empty")
	}
	if cfg.Server.HTTP.Enabled && cfg.Server.HTTP.Port == 0 {
		return fmt.Errorf("HTTP port is invalid")
	}
	if len(cfg.Accounts) == 0 {
		return fmt.Errorf("no accounts configured")
	}
	for _, acct := range cfg.Accounts {
		if acct.AccessKey == "" || acct.SecretKey == "" {
			return fmt.Errorf("AWS credentials missing for account %s", acct.AccountID)
		}
		for _, cluster := range acct.Clusters {
			if cluster.Name == "" || cluster.Region == "" {
				return fmt.Errorf("invalid cluster config: name or region missing")
			}
		}
	}
	return nil
}