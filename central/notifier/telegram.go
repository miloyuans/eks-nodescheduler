// central/notifier/telegram.go
package notifier

import (
	"central/config"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
)

var Bot *tgbotapi.BotAPI

func Init(cfg *config.GlobalConfig) error {
	var err error
	Bot, err = tgbotapi.NewBotAPI(cfg.Telegram.BotToken)
	return err
}

func Send(message string, chatIDs []int64) {
	for _, id := range chatIDs {
		msg := tgbotapi.NewMessage(id, message)
		Bot.Send(msg)
	}
}