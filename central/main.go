// central/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"sync"
	"time"

	"central/config"
	"central/core"
	"central/notifier"
	"central/processor" // ← 新增：导入 processor 包
	"central/server"
	"central/storage"
	"central/telegramlistener" // 新增：独立监听器
)

func main() {
	// 使用所有 CPU 核
	runtime.GOMAXPROCS(runtime.NumCPU())

	configFile := "config.yaml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	cfg, err := config.Load(configFile)
	if err != nil {
		log.Fatalf("[FATAL] Failed to load config %s: %v", configFile, err)
	}

	// 配置校验
	if err := validateConfig(cfg); err != nil {
		log.Fatalf("[FATAL] Config validation failed: %v", err)
	}
	log.Println("[INFO] Config loaded and validated successfully")

	// 初始化 Telegram 通知
	if err := notifier.Init(cfg); err != nil {
		log.Printf("[WARN] Telegram init failed: %v (notifications disabled)", err)
	} else {
		log.Println("[INFO] Telegram notifier initialized")
	}

	// 启动 Telegram 反馈监听（使用全局 bot）
	if len(cfg.Telegram.ChatIDs) > 0 {
		wg.Add(1)
		go telegramlistener.StartListener(ctx, &wg, cfg.Telegram.ChatIDs[0], processor.HandleTelegramFeedback)
	}

	// 初始化 MongoDB
	if err := storage.InitMongo(cfg); err != nil {
		log.Fatalf("[FATAL] MongoDB initialization failed: %v", err)
	}
	log.Println("[INFO] MongoDB initialized for all clusters")

	// 创建核心实例
	central := core.New(cfg)

	// 启动时检查并创建当天和明天空 nodegroup
	log.Println("[INFO] Performing initial daily nodegroup check...")
	for i := range cfg.Accounts {
		acct := &cfg.Accounts[i]
		for j := range acct.Clusters {
			cluster := &acct.Clusters[j]
			processor.CheckAndCreateDailyNodeGroups(central, *acct, cluster)
		}
	}
	log.Println("[INFO] Initial daily nodegroup check completed")

	// 上下文用于优雅关闭
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// 启动 Telegram 反馈监听（独立包）
	if len(cfg.Telegram.ChatIDs) > 0 {
		wg.Add(1)
		go telegramlistener.StartListener(ctx, &wg, cfg.Telegram.BotToken, cfg.Telegram.ChatIDs[0], processor.HandleTelegramFeedback)
	}

	// 启动 HTTP 服务器
	if cfg.Server.HTTP.Enabled {
		wg.Add(1)
		go server.StartHTTP(ctx, &wg, cfg, central)
	}

	// 启动每个集群的独立处理器
	for i := range cfg.Accounts {
		acct := &cfg.Accounts[i]
		for j := range acct.Clusters {
			cluster := &acct.Clusters[j]
			wg.Add(1)
			go processor.ProcessCluster(ctx, &wg, central, *acct, cluster)
		}
	}

	log.Println("[INFO] Central server started successfully")

	// 等待中断信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("[INFO] Shutdown signal received, initiating graceful shutdown...")

	// 触发关闭
	cancel()

	// 等待所有 Goroutine 完成（最多 30 秒超时）
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("[INFO] All goroutines completed")
	case <-time.After(30 * time.Second):
		log.Println("[WARN] Shutdown timeout, forcing exit")
	}

	storage.Shutdown()
	log.Println("[INFO] Central server shutdown complete")
}

func validateConfig(cfg *config.GlobalConfig) error {
	if cfg.Mongo.URI == "" {
		return fmt.Errorf("mongo.uri is required")
	}
	if cfg.Server.HTTP.Enabled && cfg.Server.HTTP.Port == 0 {
		return fmt.Errorf("server.http.port is invalid")
	}
	if len(cfg.Accounts) == 0 {
		return fmt.Errorf("no accounts configured")
	}
	for _, acct := range cfg.Accounts {
		if acct.AccessKey == "" || acct.SecretKey == "" {
			return fmt.Errorf("AWS credentials missing for account %s", acct.AccountID)
		}
		if len(acct.Clusters) == 0 {
			return fmt.Errorf("no clusters configured for account %s", acct.AccountID)
		}
		for _, cluster := range acct.Clusters {
			if cluster.Name == "" {
				return fmt.Errorf("cluster name is required")
			}
			if cluster.Region == "" {
				return fmt.Errorf("region is required for cluster %s", cluster.Name)
			}
			if cluster.NodeGroupPrefix == "" {
				return fmt.Errorf("node_group_prefix is required for cluster %s", cluster.Name)
			}
			if len(cluster.Subnets) == 0 {
				return fmt.Errorf("subnets are required for cluster %s", cluster.Name)
			}
		}
	}
	return nil
}