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
	HighThreshold   int      `yaml:"high_threshold"`   // e.g., 80
	LowThreshold    int      `yaml:"low_threshold"`    // e.g., 30
	MaxThreshold    int      `yaml:"max_threshold"`    // e.g., 70 (simulateRemoval 用)
	CooldownSeconds int      `yaml:"cooldown_seconds"` // e.g., 600
	NodeGroups      []string `yaml:"node_groups"`      // 空表示处理所有

	// 每日 nodegroup 管理参数
	NodeGroupPrefix string `yaml:"node_group_prefix"` // e.g., "autoscaler-"

	// 创建 nodegroup 时使用的实例规格
	InstanceType string `yaml:"instance_type"` // e.g., "t3.medium"
	DiskSize     int    `yaml:"disk_size"`     // GB, e.g., 20
	AmiType      string `yaml:"ami_type"`      // e.g., "AL2_x86_64"
	IamRole      string `yaml:"iam_role"`      // IAM Role ARN

	// 扩容计算阈值（添加节点后平均利用率目标）
	UtilThreshold float64 `yaml:"util_threshold"` // e.g., 0.5 (50%)

	// Agent HTTP 地址（用于下发重启指令）
	AgentEndpoint string `yaml:"agent_endpoint"` // e.g., "http://agent-svc.kube-system:8080"
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