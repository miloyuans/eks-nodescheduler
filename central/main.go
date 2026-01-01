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

	if err := notifier.Init(cfg); err != nil {
		log.Printf("Telegram init failed: %v (notifications disabled)", err)
	}

	if err := storage.InitMongo(cfg); err != nil {
		log.Fatalf("MongoDB init failed: %v", err)
	}

	central := core.New(cfg)

	var wg sync.WaitGroup

	if cfg.Server.HTTP.Enabled {
		wg.Add(1)
		go server.StartHTTP(&wg, cfg, central)
	}

	for i := range cfg.Accounts {
		acct := &cfg.Accounts[i]
		for j := range acct.Clusters {
			cluster := &acct.Clusters[j]
			wg.Add(1)
			go processor.ProcessCluster(&wg, central, *acct, cluster)
		}
	}

	log.Println("Central server (HTTP only) started successfully")
	wg.Wait()
}