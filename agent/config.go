// agent/config.go
package main // 注意：和 main.go 同包，才能让 main.go 访问 LoadConfig

import (
	"os"

	"gopkg.in/yaml.v3"
)

type AgentConfig struct {
	CentralEndpoint       string   `yaml:"central_endpoint"`
	ReportIntervalSeconds int      `yaml:"report_interval_seconds"`
	ClusterName           string   `yaml:"cluster_name"`
	NodeGroups            []string `yaml:"node_groups"`
}

// LoadConfig 加载 Agent 配置
func LoadConfig(file string) (*AgentConfig, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	var cfg AgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// 默认值
	if cfg.ReportIntervalSeconds == 0 {
		cfg.ReportIntervalSeconds = 300 // 默认 5 分钟
	}

	if cfg.CentralEndpoint == "" {
		return nil, os.ErrInvalid // 必须配置中心地址
	}

	if cfg.ClusterName == "" {
		return nil, os.ErrInvalid // 必须配置集群名
	}

	return &cfg, nil
}