// central/core/central.go
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	pb "central/proto"
	"central/config"
	"central/notifier"
	"central/storage"
)

type Central struct {
	cfg          *config.GlobalConfig
	clusterChans map[string]chan *pb.ReportRequest
	pb.UnimplementedAutoscalerServiceServer // 必须嵌入以满足接口
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
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	var req pb.ReportRequest
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
	case ch <- &req:
		if err := storage.StoreReport(req.ClusterName, &req); err != nil {
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

func (c *Central) ReportData(ctx context.Context, req *pb.ReportRequest) (*pb.ReportResponse, error) {
	if req.ClusterName == "" {
		return &pb.ReportResponse{Success: false, Message: "Missing cluster_name"}, nil
	}

	ch, ok := c.clusterChans[req.ClusterName]
	if !ok {
		return &pb.ReportResponse{Success: false, Message: fmt.Sprintf("Unknown cluster: %s", req.ClusterName)}, nil
	}

	select {
	case ch <- req:
		if err := storage.StoreReport(req.ClusterName, req); err != nil {
			fmt.Printf("Warning: store gRPC report failed: %v\n", err)
		}

		notifier.Send(
			fmt.Sprintf("[RECEIVED] gRPC report from *%s*", req.ClusterName),
			c.cfg.Telegram.ChatIDs,
		)

		return &pb.ReportResponse{Success: true, Message: "queued"}, nil
	default:
		return &pb.ReportResponse{Success: false, Message: "queue full"}, nil
	}
}

func (c *Central) GetClusterChan(name string) chan *pb.ReportRequest {
	return c.clusterChans[name]
}