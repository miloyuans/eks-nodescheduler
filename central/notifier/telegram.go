// central/notifier/telegram.go
package notifier

import (
	"log"

	"central/config"
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

// GetBot 返回全局 bot 实例（供监听器使用）
func GetBot() *tgbotapi.BotAPI {
	return bot
}

// Send 发送消息
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
		}
	}
}