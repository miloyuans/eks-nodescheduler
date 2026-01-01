// central/central.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	pb "central/proto"
	"central/config"
)

type Central struct {
	cfg          *config.GlobalConfig
	clusterChans map[string]chan *pb.ReportRequest
}

func NewCentral(cfg *config.GlobalConfig) *Central {
	c := &Central{
		cfg:          cfg,
		clusterChans: make(map[string]chan *pb.ReportRequest),
	}
	
	// 为每个集群创建 channel
	for _, acct := range cfg.Accounts {
		for _, cluster := range acct.Clusters {
			c.clusterChans[cluster.Name] = make(chan *pb.ReportRequest, 100)
		}
	}
	return c
}

// HTTP Report 处理函数
func (c *Central) httpReportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	var req pb.ReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if ch, ok := c.clusterChans[req.ClusterName]; ok {
		select {
		case ch <- &req:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]bool{"success": true})
		default:
			http.Error(w, "Queue full", http.StatusServiceUnavailable)
		}
	} else {
		http.Error(w, fmt.Sprintf("Cluster %s not found", req.ClusterName), http.StatusNotFound)
	}
}

// gRPC ReportData 方法实现
func (c *Central) ReportData(ctx context.Context, req *pb.ReportRequest) (*pb.ReportResponse, error) {
	log.Printf("Received report from cluster: %s", req.ClusterName)
	
	if ch, ok := c.clusterChans[req.ClusterName]; ok {
		select {
		case ch <- req:
			return &pb.ReportResponse{
				Success: true,
				Message: "Report queued successfully",
			}, nil
		default:
			return nil, fmt.Errorf("queue full for cluster %s", req.ClusterName)
		}
	}
	
	return nil, fmt.Errorf("cluster %s not found", req.ClusterName)
}

// 获取指定集群的 channel
func (c *Central) GetClusterChan(clusterName string) chan *pb.ReportRequest {
	return c.clusterChans[clusterName]
}