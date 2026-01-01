// central/main.go
package main

import (
	"log"
	"os"
	"sync"

	"central/config"
	"central/core"
	"central/notifier"
	"central/processor"
	"central/server"
	"central/storage"
)

func main() {
	configFile := "config.yaml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	cfg, err := config.Load(configFile)
	if err != nil {
		log.Fatalf("Failed to load config %s: %v", configFile, err)
	}

	// 初始化 Telegram 通知
	if err := notifier.Init(cfg); err != nil {
		log.Printf("Telegram init failed: %v (notifications disabled)", err)
	}

	// 初始化 MongoDB（每个集群独立 DB + TTL）
	if err := storage.InitMongo(cfg); err != nil {
		log.Fatalf("MongoDB init failed: %v", err)
	}

	// 创建核心 Central 实例
	central := core.New(cfg)

	var wg sync.WaitGroup

	// 启动 HTTP 服务器（如果启用）
	if cfg.Server.HTTP.Enabled {
		wg.Add(1)
		go server.StartHTTP(&wg, cfg, central)
	}

	// 启动 gRPC 服务器（如果启用）
	if cfg.Server.GRPC.Enabled {
		wg.Add(1)
		go server.StartGRPC(&wg, cfg, central)
	}

	// 为每个集群启动独立的处理 Goroutine
	for i := range cfg.Accounts {
		acct := &cfg.Accounts[i]
		for j := range acct.Clusters {
			cluster := &acct.Clusters[j]
			wg.Add(1)
			go processor.ProcessCluster(&wg, central, *acct, cluster)
		}
	}

	log.Println("Central server started successfully")
	wg.Wait()
}