// agent/main.go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"agent/collector"
	"agent/reporter"
	"agent/telegram"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gopkg.in/yaml.v3"
)

type AgentConfig struct {
	CentralEndpoint       string `yaml:"central_endpoint"`
	ReportIntervalSeconds int    `yaml:"report_interval_seconds"`
	ClusterName           string `yaml:"cluster_name"`
	Telegram struct {
		BotToken      string `yaml:"bot_token"`
		ControlChatID int64  `yaml:"control_chat_id"`
	} `yaml:"telegram"`
}

func LoadConfig(file string) (*AgentConfig, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	var cfg AgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if cfg.ReportIntervalSeconds <= 0 {
		cfg.ReportIntervalSeconds = 300
	}
	return &cfg, nil
}

func main() {
	configFile := "config.yaml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	cfg, err := LoadConfig(configFile)
	if err != nil {
		log.Fatalf("[FATAL] Load config failed: %v", err)
	}

	log.Printf("[INFO] Agent starting for cluster: %s", cfg.ClusterName)

	// 初始化 Telegram Bot
	bot, err := tgbotapi.NewBotAPI(cfg.Telegram.BotToken)
	if err != nil {
		log.Fatalf("[FATAL] Telegram bot init failed: %v", err)
	}
	log.Printf("[INFO] Telegram bot authorized as @%s", bot.Self.UserName)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// 启动 Telegram 监听
	wg.Add(1)
	go telegram.ListenCommands(ctx, &wg, bot, cfg.ClusterName, cfg.Telegram.ControlChatID)

	// 启动事件驱动采集
	reportChan := make(chan model.ReportRequest, 10)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := collector.InitCollector(ctx, cfg.ClusterName, reportChan); err != nil {
			log.Printf("[ERROR] Init collector failed: %v", err)
		}
	}()

	// 上报处理器
	wg.Add(1)
	go func() {
		defer wg.Done()
		for report := range reportChan {
			if err := reporter.Report(cfg.CentralEndpoint, report); err != nil {
				log.Printf("[ERROR] Report failed: %v", err)
			} else {
				log.Println("[INFO] Report sent successfully")
			}
		}
	}()

	// 启动时全量上报一次
	log.Println("[INFO] Performing initial full collection...")
	report, err := collector.CollectFull(cfg.ClusterName)
	if err != nil {
		log.Printf("[ERROR] Initial collection failed: %v", err)
	} else {
		if err := reporter.Report(cfg.CentralEndpoint, report); err != nil {
			log.Printf("[ERROR] Initial report failed: %v", err)
		} else {
			log.Println("[INFO] Initial report sent successfully")
		}
	}

	// 优雅关闭
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("[INFO] Shutdown signal received")

	cancel()
	wg.Wait()

	log.Println("[INFO] Agent shutdown complete")
}