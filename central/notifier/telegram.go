// central/notifier/telegram.go
package notifier

import (
	"context"
	"fmt"
	"log"
	"strings"

	"central/processor" // ← 新增导入 processor 处理反馈

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var bot *tgbotapi.BotAPI

func Init(cfg *config.GlobalConfig) error {
	if cfg.Telegram.BotToken == "" {
		log.Println("[WARN] Telegram BotToken empty, notification disabled")
		return nil
	}

	var err error
	bot, err = tgbotapi.NewBotAPI(cfg.Telegram.BotToken)
	if err != nil {
		return err
	}

	log.Printf("[INFO] Telegram bot authorized as @%s", bot.Self.UserName)
	return nil
}

// Send 发送消息到群
func Send(message string, chatIDs []int64) {
	if bot == nil || len(chatIDs) == 0 {
		log.Printf("[DEBUG] Telegram notification skipped: %s", message)
		return
	}

	for _, chatID := range chatIDs {
		msg := tgbotapi.NewMessage(chatID, message)
		msg.ParseMode = tgbotapi.ModeMarkdown

		if _, err := bot.Send(msg); err != nil {
			log.Printf("[ERROR] Telegram send failed to %d: %v", chatID, err)
		} else {
			log.Printf("[INFO] Telegram sent to %d: %s", chatID, message)
		}
	}
}

// InitTelegramListener 启动监听 Agent 反馈
func InitTelegramListener(botToken string, controlChatID int64) error {
	b, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		return err
	}
	bot = b

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	go func() {
		for update := range updates {
			if update.Message == nil || update.Message.Chat.ID != controlChatID {
				continue
			}

			text := strings.TrimSpace(update.Message.Text)
			if strings.HasSuffix(text, "Cordon completed") || strings.HasSuffix(text, "Restart completed") {
				parts := strings.Split(text, " ")
				if len(parts) >= 2 {
					clusterName := strings.Trim(parts[0], "[]")
					feedback := strings.Join(parts[1:], " ")
					processor.HandleTelegramFeedback(clusterName, feedback)
				}
			}
		}
	}()

	log.Println("[INFO] Telegram feedback listener started")
	return nil
}