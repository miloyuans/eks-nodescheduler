// agent/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"agent/collector"
	"agent/reporter"

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

	if cfg.CentralEndpoint == "" || cfg.ClusterName == "" {
		return nil, fmt.Errorf("central_endpoint and cluster_name are required")
	}

	if cfg.Telegram.BotToken == "" || cfg.Telegram.ControlChatID == 0 {
		return nil, fmt.Errorf("telegram bot_token and control_chat_id are required")
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

	// 初始化 Telegram Bot（与中心程序使用同一个 Bot）
	bot, err := tgbotapi.NewBotAPI(cfg.Telegram.BotToken)
	if err != nil {
		log.Fatalf("[FATAL] Telegram bot init failed: %v", err)
	}
	log.Printf("[INFO] Telegram bot authorized as @%s", bot.Self.UserName)

	// 启动 Telegram 监听（接收中心下发的指令）
	go listenForCommands(bot, cfg.ClusterName, cfg.Telegram.ControlChatID)

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
				return
			case <-ticker.C:
				report, err := collector.Collect(cfg.ClusterName, nil)
				if err != nil {
					log.Printf("[ERROR] Collect failed: %v", err)
					continue
				}
				if err := reporter.Report(cfg.CentralEndpoint, report); err != nil {
					log.Printf("[ERROR] Report failed: %v", err)
				} else {
					log.Printf("[INFO] Report sent (%d nodegroups)", len(report.NodeGroups))
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

// listenForCommands 监听控制群消息，匹配本集群指令
func listenForCommands(bot *tgbotapi.BotAPI, clusterName string, controlChatID int64) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil || update.Message.Chat.ID != controlChatID {
			continue
		}

		text := update.Message.Text
		commandPrefix := fmt.Sprintf("[%s] /restart", clusterName)

		if strings.HasPrefix(text, commandPrefix) {
			log.Printf("[RESTART] Received restart command from central")

			// TODO: 在这里执行你的服务重启逻辑，例如：
			// kubectl rollout restart deployment/my-app -n my-ns
			// 或执行自定义脚本

			// 模拟重启完成（实际替换为真实逻辑）
			time.Sleep(10 * time.Second)

			// 反馈完成
			msg := tgbotapi.NewMessage(controlChatID, fmt.Sprintf("[%s] Restart completed successfully", clusterName))
			if _, err := bot.Send(msg); err != nil {
				log.Printf("[ERROR] Failed to send restart feedback: %v", err)
			} else {
				log.Println("[RESTART] Restart feedback sent")
			}
		}
	}
}