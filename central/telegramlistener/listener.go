// central/telegramlistener/listener.go
package telegramlistener

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// FeedbackHandler 处理 Agent 反馈的回调函数
type FeedbackHandler func(clusterName, feedback string)

var handler FeedbackHandler

// Init 启动 Telegram 监听
func Init(ctx context.Context, wg *sync.WaitGroup, botToken string, controlChatID int64, h FeedbackHandler) {
	defer wg.Done()

	handler = h

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Printf("[ERROR] Telegram bot init failed: %v", err)
		return
	}
	log.Printf("[INFO] Telegram feedback listener started (chat ID: %d)", controlChatID)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			log.Println("[INFO] Telegram feedback listener shutting down")
			return
		case update := <-updates:
			if update.Message == nil || update.Message.Chat.ID != controlChatID {
				continue
			}

			text := strings.TrimSpace(update.Message.Text)
			if strings.HasSuffix(text, "Cordon completed") || strings.HasSuffix(text, "Restart completed") {
				parts := strings.Split(text, " ")
				if len(parts) >= 2 {
					clusterName := strings.Trim(parts[0], "[]")
					feedback := strings.Join(parts[1:], " ")
					if handler != nil {
						handler(clusterName, feedback)
					}
				}
			}
		}
	}
}