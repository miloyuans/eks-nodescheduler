// central/core/central.go
package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"central/config"
	"central/notifier"
	"central/storage"
)

type Central struct {
	cfg          *config.GlobalConfig
	clusterChans map[string]chan ReportRequest // 简化：不依赖 proto
}

type ReportRequest struct {
	ClusterName string         `json:"cluster_name"`
	NodeGroups  []NodeGroupData `json:"node_groups"`
	Timestamp   int64          `json:"timestamp"`
}

type NodeGroupData struct {
	Name        string            `json:"name"`
	AsgName     string            `json:"asg_name"`
	MinSize     int32             `json:"min_size"`
	MaxSize     int32             `json:"max_size"`
	DesiredSize int32             `json:"desired_size"`
	NodeUtils   map[string]float64 `json:"node_utils"`
	Nodes       []NodeInfo        `json:"nodes"`
}

type NodeInfo struct {
	Name                string `json:"name"`
	InstanceId          string `json:"instance_id"`
	RequestCpuMilli     int64  `json:"request_cpu_milli"`
	AllocatableCpuMilli int64  `json:"allocatable_cpu_milli"`
}

func New(cfg *config.GlobalConfig) *Central {
	c := &Central{
		cfg:          cfg,
		clusterChans: make(map[string]chan ReportRequest),
	}

	for _, acct := range cfg.Accounts {
		for _, cluster := range acct.Clusters {
			c.clusterChans[cluster.Name] = make(chan ReportRequest, 100)
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

	var req ReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if req.ClusterName == "" {
		http.Error(w, "Missing cluster_name", http.StatusBadRequest)
		return
	}

	ch, ok := c.clusterChans[req.ClusterName]
	if !ok {
		http.Error(w, fmt.Sprintf("Unknown cluster: %s", req.ClusterName), http.StatusNotFound)
		return
	}

	select {
	case ch <- req:
		if err := storage.StoreReport(req.ClusterName, req); err != nil {
			fmt.Printf("Warning: store report failed: %v\n", err)
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

func (c *Central) GetClusterChan(name string) chan ReportRequest {
	return c.clusterChans[name]
}