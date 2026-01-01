// central/main.go
package main

import (
	"context"
	"log"
	"sync"

	"central/config"
	"central/server"
	"central/storage"
)

func main() {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Load config failed: %v", err)
	}

	// 初始化 Mongo（为每个 cluster 创建 DB + TTL）
	if err := storage.InitMongo(cfg); err != nil {
		log.Fatalf("Init Mongo failed: %v", err)
	}

	central := &Central{
		cfg: cfg,
	}

	var wg sync.WaitGroup

	// 启动 HTTP/gRPC 服务器
	if cfg.Server.HTTP.Enabled {
		wg.Add(1)
		go server.StartHTTP(&wg, cfg, central)
	}
	if cfg.Server.GRPC.Enabled {
		wg.Add(1)
		go server.StartGRPC(&wg, cfg, central)
	}

	// 启动每个集群的独立处理器
	for _, acct := range cfg.Accounts {
		for i := range acct.Clusters {
			cluster := &acct.Clusters[i] // 指针避免拷贝
			wg.Add(1)
			go central.startClusterProcessor(&wg, acct, cluster)
		}
	}

	wg.Wait()
}