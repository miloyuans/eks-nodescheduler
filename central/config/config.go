// central/config/config.go
package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type GlobalConfig struct {
	Server struct {
		HTTP struct {
			Enabled bool   `yaml:"enabled"`
			Addr    string `yaml:"addr"`
			Port    int    `yaml:"port"`
		} `yaml:"http"`
	} `yaml:"server"`
	Whitelist []string `yaml:"whitelist"`
	Mongo struct {
		URI     string `yaml:"uri"`
		TTLDays int    `yaml:"ttl_days"`
	} `yaml:"mongo"`
	Telegram struct {
		BotToken string  `yaml:"bot_token"`
		ChatIDs  []int64 `yaml:"chat_ids"`
	} `yaml:"telegram"`
	Accounts []AccountConfig `yaml:"accounts"`
}

type AccountConfig struct {
	AccountID string `yaml:"account_id"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Clusters  []ClusterConfig `yaml:"clusters"`
}

type ClusterConfig struct {
	Name            string   `yaml:"name"`
	Region          string   `yaml:"region"`
	HighThreshold   int      `yaml:"high_threshold"`
	LowThreshold    int      `yaml:"low_threshold"`
	MaxThreshold    int      `yaml:"max_threshold"`
	CooldownSeconds int      `yaml:"cooldown_seconds"`
	NodeGroups      []string `yaml:"node_groups"`
	NodeGroupPrefix string   `yaml:"node_group_prefix"`
	InstanceType    string   `yaml:"instance_type"`
	DiskSize        int      `yaml:"disk_size"`
	AmiType         string   `yaml:"ami_type"`
	IamRole         string   `yaml:"iam_role"`
	UtilThreshold   float64  `yaml:"util_threshold"` // e.g., 0.5 for 50%
}

func Load(file string) (*GlobalConfig, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var cfg GlobalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}