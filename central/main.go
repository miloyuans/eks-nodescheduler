// central/main.go
package main

import (
	"log"
	"sync"

	"central/config"
	"central/notifier"
	"central/processor"
	"central/server"
	"central/storage"
)

func main() {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Load config failed: %v", err)
	}

	if err := notifier.Init(cfg); err != nil {
		log.Fatalf("Init notifier failed: %v", err)
	}

	if err := storage.InitMongo(cfg); err != nil {
		log.Fatalf("Init Mongo failed: %v", err)
	}

	// 创建 Central 实例
	central := NewCentral(cfg)  // 使用我们定义的 NewCentral

	var wg sync.WaitGroup

	if cfg.Server.HTTP.Enabled {
		wg.Add(1)
		go server.StartHTTP(&wg, cfg, central)
	}
	if cfg.Server.GRPC.Enabled {
		wg.Add(1)
		go server.StartGRPC(&wg, cfg, central)
	}

	// 启动集群处理器
	for _, acct := range cfg.Accounts {
		for i := range acct.Clusters {
			cluster := &acct.Clusters[i]
			wg.Add(1)
			go processor.ProcessCluster(&wg, central, acct, cluster)
		}
	}

	wg.Wait()
}