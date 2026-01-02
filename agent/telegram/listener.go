// agent/telegram/listener.go
package telegram

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ListenCommands 监听 Telegram 群消息，执行指令
func ListenCommands(ctx context.Context, wg *sync.WaitGroup, bot *tgbotapi.BotAPI, clusterName string, controlChatID int64) {
	defer wg.Done()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	cordonCommand := fmt.Sprintf("[%s] /cordon", clusterName)
	restartCommand := fmt.Sprintf("[%s] /restart", clusterName)

	for {
		select {
		case <-ctx.Done():
			log.Println("[TELEGRAM] Listener shutting down")
			return
		case update := <-updates:
			if update.Message == nil || update.Message.Chat.ID != controlChatID {
				continue
			}

			text := strings.TrimSpace(update.Message.Text)
			if text == cordonCommand {
				log.Printf("[COMMAND] Received cordon command for %s", clusterName)

				// 发送开始通知
				msg := tgbotapi.NewMessage(controlChatID, fmt.Sprintf("[%s] Starting cordon old nodes...", clusterName))
				bot.Send(msg)

				// 执行 Cordon（模拟，实际替换为 k8s client cordon 旧节点）
				go executeCordon(clusterName, bot, controlChatID)
			} else if text == restartCommand {
				log.Printf("[COMMAND] Received restart command for %s", clusterName)

				msg := tgbotapi.NewMessage(controlChatID, fmt.Sprintf("[%s] Starting services restart...", clusterName))
				bot.Send(msg)

				go executeRestart(clusterName, bot, controlChatID)
			}
		}
	}
}

func executeCordon(clusterName string, bot *tgbotapi.BotAPI, controlChatID int64) {
	log.Println("[CORDON] Executing cordon old nodes...")

	// TODO: 实际执行 cordon 旧节点（使用 k8s client）

	time.Sleep(10 * time.Second) // 模拟

	feedback := fmt.Sprintf("[%s] Cordon completed", clusterName)
	msg := tgbotapi.NewMessage(controlChatID, feedback)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("[ERROR] Failed to send cordon feedback: %v", err)
	} else {
		log.Printf("[CORDON] Feedback sent: %s", feedback)
	}
}

func executeRestart(clusterName string, bot *tgbotapi.BotAPI, controlChatID int64) {
	log.Println("[RESTART] Executing services restart...")

	// TODO: 实际执行 rollout restart

	time.Sleep(30 * time.Second) // 模拟

	feedback := fmt.Sprintf("[%s] Restart completed", clusterName)
	msg := tgbotapi.NewMessage(controlChatID, feedback)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("[ERROR] Failed to send restart feedback: %v", err)
	} else {
		log.Printf("[RESTART] Feedback sent: %s", feedback)
	}
}