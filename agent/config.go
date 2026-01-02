// agent/config.go
package main

import (
	"os"

	"gopkg.in/yaml.v3"
)

type AgentConfig struct {
	CentralEndpoint        string   `yaml:"central_endpoint"`
	ReportIntervalSeconds  int      `yaml:"report_interval_seconds"`
	ClusterName            string   `yaml:"cluster_name"`
	NodeGroups             []string `yaml:"node_groups"`
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
	if cfg.ReportIntervalSeconds == 0 {
		cfg.ReportIntervalSeconds = 300 // 默认5分钟
	}
	return &cfg, nil
}