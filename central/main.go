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
	"central/processor"
	"central/server"
	"central/storage"
	"central/telegramlistener" // Imported normally, no alias
)

func main() {
	// Use all CPU cores
	runtime.GOMAXPROCS(runtime.NumCPU())

	configFile := "config.yaml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	cfg, err := config.Load(configFile)
	if err != nil {
		log.Fatalf("[FATAL] Failed to load config %s: %v", configFile, err)
	}

	// Config validation
	if err := validateConfig(cfg); err != nil {
		log.Fatalf("[FATAL] Config validation failed: %v", err)
	}
	log.Println("[INFO] Config loaded and validated successfully")

	// Initialize Telegram notifier
	if err := notifier.Init(cfg); err != nil {
		log.Printf("[WARN] Telegram init failed: %v (notifications disabled)", err)
	} else {
		log.Println("[INFO] Telegram notifier initialized")
	}

	// Initialize MongoDB
	if err := storage.InitMongo(cfg); err != nil {
		log.Fatalf("[FATAL] MongoDB initialization failed: %v", err)
	}
	log.Println("[INFO] MongoDB initialized for all clusters")

	// Create core instance
	central := core.New(cfg)

	// Initial daily nodegroup check
	log.Println("[INFO] Performing initial daily nodegroup check...")
	for i := range cfg.Accounts {
		acct := &cfg.Accounts[i]
		for j := range acct.Clusters {
			cluster := &acct.Clusters[j]
			processor.CheckAndCreateDailyNodeGroups(central, *acct, cluster)
		}
	}
	log.Println("[INFO] Initial daily nodegroup check completed")

	// Context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// Start Telegram feedback listener (using the correct package name)
	if len(cfg.Telegram.ChatIDs) > 0 {
		wg.Add(1)
		go telegramlistener.StartListener(ctx, &wg, cfg.Telegram.BotToken, cfg.Telegram.ChatIDs[0], processor.HandleTelegramFeedback)
	}

	// Start HTTP server
	if cfg.Server.HTTP.Enabled {
		wg.Add(1)
		go server.StartHTTP(ctx, &wg, cfg, central)
	}

	// Start independent processors for each cluster
	for i := range cfg.Accounts {
		acct := &cfg.Accounts[i]
		for j := range acct.Clusters {
			cluster := &acct.Clusters[j]
			wg.Add(1)
			go processor.ProcessCluster(ctx, &wg, central, *acct, cluster)
		}
	}

	log.Println("[INFO] Central server started successfully")

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("[INFO] Shutdown signal received, initiating graceful shutdown...")

	cancel()

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