// central/core/central.go
package core

import (
	"encoding/json"
	"fmt"
	"net/http"

	"central/config"
	"central/model"
	"central/notifier"
	"central/storage"
)

type Central struct {
	cfg          *config.GlobalConfig
	clusterChans map[string]chan model.ReportRequest
}

func New(cfg *config.GlobalConfig) *Central {
	c := &Central{
		cfg:          cfg,
		clusterChans: make(map[string]chan model.ReportRequest),
	}

	for _, acct := range cfg.Accounts {
		for _, cluster := range acct.Clusters {
			c.clusterChans[cluster.Name] = make(chan model.ReportRequest, 100)
		}
	}
	return c
}

func (c *Central) GetTelegramChatIDs() []int64 {
	return c.cfg.Telegram.ChatIDs
}

func (c *Central) HTTPReportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	var req model.ReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if req.ClusterName == "" {
		http.Error(w, "Missing cluster_name", http.StatusBadRequest)
		return
	}

	// 检查是否匹配配置中的集群
	found := false
	for _, acct := range c.cfg.Accounts {
		for _, cluster := range acct.Clusters {
			if cluster.Name == req.ClusterName {
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		msg := fmt.Sprintf("[WARN] Received report from unknown cluster: %s", req.ClusterName)
		log.Println(msg)
		notifier.Send(msg, c.cfg.Telegram.ChatIDs)
		http.Error(w, "Unknown cluster", http.StatusNotFound)
		return
	}

	ch, ok := c.clusterChans[req.ClusterName]
	if !ok {
		http.Error(w, "Internal error: channel not found", http.StatusInternalServerError)
		return
	}

	select {
	case ch <- req:
		if err := storage.StoreReport(req.ClusterName, req); err != nil {
			log.Printf("[WARN] Failed to store report for %s: %v", req.ClusterName, err)
		}

		notifier.Send(
			fmt.Sprintf("[RECEIVED] Report from cluster *%s* (%d nodegroups)", req.ClusterName, len(req.NodeGroups)),
			c.cfg.Telegram.ChatIDs,
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "queued"})
	default:
		http.Error(w, "Server busy, queue full", http.StatusServiceUnavailable)
	}
}

func (c *Central) GetClusterChan(name string) chan model.ReportRequest {
	return c.clusterChans[name]
}