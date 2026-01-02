// agent/main.go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"sync"
	"time"

	"agent/collector"
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
	log.Printf("[INFO] Report interval: %d seconds", cfg.ReportIntervalSeconds)
	log.Printf("[INFO] Telegram BotToken loaded, ControlChatID: %d", cfg.Telegram.ControlChatID)

	// 初始化 Telegram Bot
	bot, err := tgbotapi.NewBotAPI(cfg.Telegram.BotToken)
	if err != nil {
		log.Fatalf("[FATAL] Telegram bot init failed: %v", err)
	}
	log.Printf("[INFO] Telegram bot authorized as @%s", bot.Self.UserName)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// 启动 Telegram 监听器（异步）
	wg.Add(1)
	go listenTelegramCommands(ctx, &wg, bot, cfg.ClusterName, cfg.Telegram.ControlChatID)

	// 启动事件驱动采集器
	reportChan := make(chan model.ReportRequest, 10)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := collector.InitCollector(ctx, cfg.ClusterName, cfg.NodeGroups, reportChan); err != nil {
			log.Printf("[ERROR] Init collector failed: %v", err)
		}
	}()

	// 启动上报处理器（异步）
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

	// 启动时立即全量上报一次
	log.Println("[INFO] Performing initial full collection...")
	report, err := collector.CollectFull(cfg.ClusterName, cfg.NodeGroups)
	if err != nil {
		log.Printf("[ERROR] Initial collection failed: %v", err)
	} else {
		reportChan <- report
		log.Println("[INFO] Initial report queued")
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

// listenTelegramCommands 监听 Telegram 群消息，收到指令执行重启
func listenTelegramCommands(ctx context.Context, wg *sync.WaitGroup, bot *tgbotapi.BotAPI, clusterName string, controlChatID int64) {
	defer wg.Done()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			log.Println("[INFO] Telegram listener shutting down")
			return
		case update := range updates {
			if update.Message == nil || update.Message.Chat.ID != controlChatID {
				continue
			}

			text := update.Message.Text
			commandPrefix := fmt.Sprintf("[%s] /restart", clusterName)

			if strings.HasPrefix(text, commandPrefix) {
				log.Printf("[INFO] Received restart command from central for %s", clusterName)

				// 发送正在执行通知
				msg := tgbotapi.NewMessage(controlChatID, fmt.Sprintf("[%s] Starting services restart...", clusterName))
				bot.Send(msg)

				// 执行重启逻辑（异步）
				go restartServices(clusterName, bot, controlChatID)

				log.Println("[INFO] Restart process started asynchronously")
			}
		}
	}
}

// restartServices 顺序重启 State fulSet > Deployment，等待 Pod Running
func restartServices(clusterName string, bot *tgbotapi.BotAPI, controlChatID int64) {
	// 获取 k8s client
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Printf("[ERROR] Failed to get k8s config: %v", err)
		sendTelegramFeedback(bot, controlChatID, clusterName, "failed")
		return
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Printf("[ERROR] Failed to create k8s client: %v", err)
		sendTelegramFeedback(bot, controlChatID, clusterName, "failed")
		return
	}

	// 优先重启 State fulSet
	statefulSets, err := clientset.AppsV1().StatefulSets("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Printf("[ERROR] Failed to list StatefulSets: %v", err)
		sendTelegramFeedback(bot, controlChatID, clusterName, "failed")
		return
	}

	for _, ss := range statefulSets.Items {
		log.Printf("[INFO] Restarting StatefulSet %s/%s", ss.Namespace, ss.Name)
		if err := restartAndWait(clientset, ss.Namespace, ss.Name, "StatefulSet"); err != nil {
			log.Printf("[ERROR] Restart StatefulSet %s/%s failed: %v", ss.Namespace, ss.Name, err)
			sendTelegramFeedback(bot, controlChatID, clusterName, "failed")
			return
		}
	}

	// 然后重启 Deployment
	deployments, err := clientset.AppsV1().Deployments("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Printf("[ERROR] Failed to list Deployments: %v", err)
		sendTelegramFeedback(bot, controlChatID, clusterName, "failed")
		return
	}

	for _, dep := range deployments.Items {
		log.Printf("[INFO] Restarting Deployment %s/%s", dep.Namespace, dep.Name)
		if err := restartAndWait(clientset, dep.Namespace, dep.Name, "Deployment"); err != nil {
			log.Printf("[ERROR] Restart Deployment %s/%s failed: %v", dep.Namespace, dep.Name, err)
			sendTelegramFeedback(bot, controlChatID, clusterName, "failed")
			return
		}
	}

	log.Println("[INFO] All services restarted successfully")
	sendTelegramFeedback(bot, controlChatID, clusterName, "success")
}

// restartAndWait 重启并等待 Pod Running
func restartAndWait(clientset *kubernetes.Clientset, namespace, name, kind string) error {
	// 执行 rollout restart
	cmd := fmt.Sprintf("kubectl rollout restart %s %s -n %s", strings.ToLower(kind), name, namespace)
	// 这里使用 os.Exec 执行 kubectl（假设 kubectl 可用，或用 clientset API 实现 rollout）
	exec.Command("sh", "-c", cmd).Run()

	// 等待 Pod Ready
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
			LabelSelector: "app=" + name, // 假设 label 匹配
		})
		if err != nil {
			return err
		}

		allReady := true
		for _, pod := range pods.Items {
			if pod.Status.Phase != v1.PodRunning {
				allReady = false
				break
			}
		}
		if allReady {
			return nil
		}
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("timeout waiting for %s %s/%s ready", kind, namespace, name)
}

// sendTelegramFeedback 发送反馈到 Telegram 群
func sendTelegramFeedback(bot *tgbotapi.BotAPI, chatID int64, clusterName, status string) {
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("[%s] Restart %s", clusterName, status))
	_, err := bot.Send(msg)
	if err != nil {
		log.Printf("[ERROR] Failed to send Telegram feedback: %v", err)
	} else {
		log.Printf("[INFO] Telegram feedback sent: Restart %s for %s", status, clusterName)
	}
}