// config/config.go (unchanged)
package config

import (
	"io/ioutil"
	"log"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ClusterName     string   `yaml:"clusterName"`
	HighThreshold   int      `yaml:"highThreshold"`
	LowThreshold    int      `yaml:"lowThreshold"`
	MaxThreshold    int      `yaml:"maxThreshold"`
	CooldownSeconds int      `yaml:"cooldownSeconds"`
	NodeGroups      []string `yaml:"nodeGroups"`
}

func LoadConfig(file string) (Config, error) {
	data, err := ioutil.ReadFile(file)
	if err != nil {
		return Config{}, err
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return Config{}, err
	}

	if config.HighThreshold == 0 {
		config.HighThreshold = 80
	}
	if config.LowThreshold == 0 {
		config.LowThreshold = 30
	}
	if config.MaxThreshold == 0 {
		config.MaxThreshold = 70
	}
	if config.CooldownSeconds == 0 {
		config.CooldownSeconds = 300
	}

	log.Printf("Loaded config: %+v", config)
	return config, nil
}