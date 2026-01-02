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
	"agent/model"
	"agent/reporter"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gopkg.in/yaml.v3"
)

type AgentConfig struct {
	CentralEndpoint       string   `yaml:"central_endpoint"`
	ReportIntervalSeconds int      `yaml:"report_interval_seconds"`
	ClusterName           string   `yaml:"cluster_name"`
	NodeGroups            []string `yaml:"node_groups"`
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
	log.Printf("[INFO] Central endpoint: %s", cfg.CentralEndpoint)

	// 初始化 Telegram Bot（仅用于接收指令和发送反馈）
	bot, err := tgbotapi.NewBotAPI(cfg.Telegram.BotToken)
	if err != nil {
		log.Fatalf("[FATAL] Telegram bot init failed: %v", err)
	}
	log.Printf("[INFO] Telegram bot authorized as @%s", bot.Self.UserName)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// 启动 Telegram 指令监听（接收中心下发的重启指令）
	wg.Add(1)
	go listenTelegramCommands(ctx, &wg, bot, cfg.ClusterName, cfg.Telegram.ControlChatID)

	// 启动事件驱动采集
	reportChan := make(chan model.ReportRequest, 10)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := collector.InitCollector(ctx, cfg.ClusterName, cfg.NodeGroups, reportChan); err != nil {
			log.Printf("[ERROR] Init collector failed: %v", err)
		}
	}()

	// 上报处理器：只打印日志，不发 Telegram 通知
	wg.Add(1)
	go func() {
		defer wg.Done()
		for report := range reportChan {
			// 只打印日志，不发通知
			log.Printf("[REPORT] Starting to send report (%d nodegroups)", len(report.NodeGroups))

			if err := reporter.Report(cfg.CentralEndpoint, report); err != nil {
				log.Printf("[REPORT] Failed to send report: %v", err)
			} else {
				log.Printf("[REPORT] Successfully sent report (%d nodegroups)", len(report.NodeGroups))
			}
		}
	}()

	// 启动时立即全量上报一次（只日志）
	log.Println("[INFO] Performing initial full collection...")
	report, err := collector.CollectFull(cfg.ClusterName, cfg.NodeGroups)
	if err != nil {
		log.Printf("[ERROR] Initial collection failed: %v", err)
	} else {
		log.Printf("[REPORT] Initial report ready (%d nodegroups)", len(report.NodeGroups))
		if err := reporter.Report(cfg.CentralEndpoint, report); err != nil {
			log.Printf("[REPORT] Initial report failed: %v", err)
		} else {
			log.Println("[REPORT] Initial report sent successfully")
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

// listenTelegramCommands 监听 Telegram 群消息，只响应精确匹配的 /restart 指令
func listenTelegramCommands(ctx context.Context, wg *sync.WaitGroup, bot *tgbotapi.BotAPI, clusterName string, controlChatID int64) {
	defer wg.Done()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	expectedCommand := fmt.Sprintf("[%s] /restart", clusterName)

	for {
		select {
		case <-ctx.Done():
			log.Println("[INFO] Telegram listener shutting down")
			return
		case update := <-updates:
			if update.Message == nil || update.Message.Chat.ID != controlChatID {
				continue
			}

			text := strings.TrimSpace(update.Message.Text)
			if text == expectedCommand {
				log.Printf("[RESTART] Received valid restart command for %s", clusterName)

				// 发送开始执行通知
				msg := tgbotapi.NewMessage(controlChatID, fmt.Sprintf("[%s] Starting services restart...", clusterName))
				bot.Send(msg)

				// 异步执行重启
				go restartServices(clusterName, bot, controlChatID)
			}
		}
	}
}

// restartServices 执行重启逻辑（请替换为你的实际重启命令）
func restartServices(clusterName string, bot *tgbotapi.BotAPI, controlChatID int64) {
	log.Println("[RESTART] Executing services restart...")

	// TODO: 替换为你的实际重启逻辑，例如：
	// kubectl rollout restart deployment/... -n ...
	// 或执行自定义脚本

	// 模拟重启耗时
	time.Sleep(30 * time.Second)

	// 假设成功（实际根据执行结果判断）
	success := true

	status := "success"
	if !success {
		status = "failed"
	}

	// 仅在执行完成后发送反馈
	feedback := fmt.Sprintf("[%s] Restart %s", clusterName, status)
	msg := tgbotapi.NewMessage(controlChatID, feedback)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("[ERROR] Failed to send restart feedback: %v", err)
	} else {
		log.Printf("[RESTART] Feedback sent: %s", feedback)
	}
}