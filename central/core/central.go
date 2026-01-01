// central/core/central.go
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	pb "central/proto"
	"central/config"
	"central/notifier"
	"central/storage"
)

type Central struct {
	cfg          *config.GlobalConfig
	clusterChans map[string]chan *pb.ReportRequest
}

func New(cfg *config.GlobalConfig) *Central {
	c := &Central{
		cfg:          cfg,
		clusterChans: make(map[string]chan *pb.ReportRequest),
	}

	for _, acct := range cfg.Accounts {
		for _, cluster := range acct.Clusters {
			c.clusterChans[cluster.Name] = make(chan *pb.ReportRequest, 100)
		}
	}
	return c
}

func (c *Central) HTTPReportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST", http.StatusMethodNotAllowed)
		return
	}

	var req pb.ReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if ch, ok := c.clusterChans[req.ClusterName]; ok {
		select {
		case ch <- &req:
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]bool{"success": true})
		default:
			http.Error(w, "Queue full", http.StatusServiceUnavailable)
		}
	} else {
		http.Error(w, "Cluster not found", http.StatusNotFound)
	}
}

func (c *Central) ReportData(ctx context.Context, req *pb.ReportRequest) (*pb.ReportResponse, error) {
	if ch, ok := c.clusterChans[req.ClusterName]; ok {
		select {
		case ch <- req:
			storage.StoreReport(req.ClusterName, req)
			return &pb.ReportResponse{Success: true, Message: "Queued"}, nil
		default:
			return nil, fmt.Errorf("queue full")
		}
	}
	return nil, fmt.Errorf("cluster not found")
}

func (c *Central) GetClusterChan(name string) chan *pb.ReportRequest {
	return c.clusterChans[name]
}