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

func ListenCommands(ctx context.Context, wg *sync.WaitGroup, bot *tgbotapi.BotAPI, clusterName string, controlChatID int64) {
	defer wg.Done()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	expectedCommand := fmt.Sprintf("[%s] /restart", clusterName)

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
			if text == expectedCommand {
				log.Printf("[RESTART] Received restart command for %s", clusterName)

				// 发送开始通知
				msg := tgbotapi.NewMessage(controlChatID, fmt.Sprintf("[%s] Starting services restart...", clusterName))
				bot.Send(msg)

				// 执行重启（异步）
				go executeRestart(clusterName, bot, controlChatID)
			}
		}
	}
}

func executeRestart(clusterName string, bot *tgbotapi.BotAPI, controlChatID int64) {
	log.Println("[RESTART] Executing services restart...")

	// TODO: 替换为实际重启逻辑
	time.Sleep(30 * time.Second) // 模拟

	success := true // 实际根据结果判断

	status := "success"
	if !success {
		status = "failed"
	}

	feedback := fmt.Sprintf("[%s] Restart %s", clusterName, status)
	msg := tgbotapi.NewMessage(controlChatID, feedback)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("[ERROR] Failed to send feedback: %v", err)
	} else {
		log.Printf("[RESTART] Feedback sent: %s", feedback)
	}
}