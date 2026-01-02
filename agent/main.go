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

	"agent/collector" // ← 新增：导入 collector 包
	"agent/model"
	"agent/reporter"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gopkg.in/yaml.v3"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels" // ← 新增：导入 labels
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd" // 如果需要 out-of-cluster config
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

	bot, err := tgbotapi.NewBotAPI(cfg.Telegram.BotToken)
	if err != nil {
		log.Fatalf("[FATAL] Telegram bot init failed: %v", err)
	}
	log.Printf("[INFO] Telegram bot authorized as @%s", bot.Self.UserName)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// 启动 Telegram 监听
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
	report, err := collector.CollectFull(cfg.ClusterName, cfg.NodeGroups)
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

// listenTelegramCommands 监听指令
func listenTelegramCommands(ctx context.Context, wg *sync.WaitGroup, bot *tgbotapi.BotAPI, clusterName string, controlChatID int64) {
	defer wg.Done()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			return
		case update := <-updates:
			if update.Message == nil || update.Message.Chat.ID != controlChatID {
				continue
			}

			text := update.Message.Text
			if strings.HasPrefix(text, fmt.Sprintf("[%s] /restart", clusterName)) {
				log.Printf("[RESTART] Received restart command for %s", clusterName)

				msg := tgbotapi.NewMessage(controlChatID, fmt.Sprintf("[%s] Starting services restart...", clusterName))
				bot.Send(msg)

				go restartServices(clusterName, bot, controlChatID)
			}
		}
	}
}

// restartServices 顺序重启 StatefulSet → Deployment
func restartServices(clusterName string, bot *tgbotapi.BotAPI, controlChatID int64) {
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Printf("[ERROR] k8s config failed: %v", err)
		sendTelegramFeedback(bot, controlChatID, clusterName, "failed")
		return
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Printf("[ERROR] k8s clientset failed: %v", err)
		sendTelegramFeedback(bot, controlChatID, clusterName, "failed")
		return
	}

	// 重启 StatefulSet
	statefulSets, err := clientset.AppsV1().StatefulSets("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Printf("[ERROR] List StatefulSets failed: %v", err)
		sendTelegramFeedback(bot, controlChatID, clusterName, "failed")
		return
	}

	for _, ss := range statefulSets.Items {
		log.Printf("[RESTART] Restarting StatefulSet %s/%s", ss.Namespace, ss.Name)
		if err := restartAndWait(clientset, ss.Namespace, ss.Name, "statefulset"); err != nil {
			log.Printf("[ERROR] Restart StatefulSet failed: %v", err)
			sendTelegramFeedback(bot, controlChatID, clusterName, "failed")
			return
		}
	}

	// 重启 Deployment
	deployments, err := clientset.AppsV1().Deployments("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Printf("[ERROR] List Deployments failed: %v", err)
		sendTelegramFeedback(bot, controlChatID, clusterName, "failed")
		return
	}

	for _, dep := range deployments.Items {
		log.Printf("[RESTART] Restarting Deployment %s/%s", dep.Namespace, dep.Name)
		if err := restartAndWait(clientset, dep.Namespace, dep.Name, "deployment"); err != nil {
			log.Printf("[ERROR] Restart Deployment failed: %v", err)
			sendTelegramFeedback(bot, controlChatID, clusterName, "failed")
			return
		}
	}

	log.Println("[RESTART] All services restarted successfully")
	sendTelegramFeedback(bot, controlChatID, clusterName, "success")
}

// restartAndWait 执行 rollout restart 并等待 Pod Ready
func restartAndWait(clientset *kubernetes.Clientset, namespace, name, kind string) error {
	patchData := []byte(fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"%s"}}}}`, time.Now().Format(time.RFC3339)))

	var err error
	if kind == "deployment" {
		_, err = clientset.AppsV1().Deployments(namespace).Patch(context.TODO(), name, types.StrategicMergePatchType, patchData, metav1.PatchOptions{})
	} else {
		_, err = clientset.AppsV1().StatefulSets(namespace).Patch(context.TODO(), name, types.StrategicMergePatchType, patchData, metav1.PatchOptions{})
	}
	if err != nil {
		return err
	}

	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		var pods *v1.PodList
		var err error
		if kind == "deployment" {
			dep, _ := clientset.AppsV1().Deployments(namespace).Get(context.TODO(), name, metav1.GetOptions{})
			selector := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
			pods, err = clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: selector.String()})
		} else {
			ss, _ := clientset.AppsV1().StatefulSets(namespace).Get(context.TODO(), name, metav1.GetOptions{})
			selector := labels.SelectorFromSet(ss.Spec.Selector.MatchLabels)
			pods, err = clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: selector.String()})
		}
		if err != nil {
			return err
		}

		allReady := true
		for _, pod := range pods.Items {
			if pod.Status.Phase != v1.PodRunning || pod.DeletionTimestamp != nil {
				allReady = false
				break
			}
			for _, cond := range pod.Status.Conditions {
				if cond.Type == v1.PodReady && cond.Status != v1.ConditionTrue {
					allReady = false
					break
				}
			}
		}
		if allReady {
			return nil
		}
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("timeout waiting for %s %s/%s ready", kind, namespace, name)
}

// sendTelegramFeedback 发送反馈
func sendTelegramFeedback(bot *tgbotapi.BotAPI, chatID int64, clusterName, status string) {
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("[%s] Restart %s", clusterName, status))
	if _, err := bot.Send(msg); err != nil {
		log.Printf("[ERROR] Telegram feedback failed: %v", err)
	}
}