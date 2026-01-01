// central/core/central.go
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

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

	// 为配置中的每个集群创建独立的 buffered channel
	for _, acct := range cfg.Accounts {
		for _, cluster := range acct.Clusters {
			c.clusterChans[cluster.Name] = make(chan *pb.ReportRequest, 100)
		}
	}
	return c
}

// HTTPReportHandler 处理 Agent 通过 HTTP POST /report 上报的数据
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

	// 异步入队 + 持久化
	select {
	case ch <- &req:
		// 成功入队后持久化到 MongoDB
		if err := storage.StoreReport(req.ClusterName, &req); err != nil {
			// 持久化失败不影响主流程，只记录日志
			fmt.Printf("Warning: failed to store report for %s: %v\n", req.ClusterName, err)
		}

		// 可选：发送通知表示成功接收
		notifier.Send(
			fmt.Sprintf("[RECEIVED] Report from cluster *%s* at %s", req.ClusterName, time.Now().Format("15:04:05")),
			c.cfg.Telegram.ChatIDs,
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "Report queued"})
	default:
		http.Error(w, "Server busy, queue full", http.StatusServiceUnavailable)
	}
}

// ReportData 实现 gRPC 服务方法
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
		// 持久化
		if err := storage.StoreReport(req.ClusterName, req); err != nil {
			fmt.Printf("Warning: failed to store gRPC report for %s: %v\n", req.ClusterName, err)
		}

		// 通知
		notifier.Send(
			fmt.Sprintf("[RECEIVED] gRPC report from cluster *%s*", req.ClusterName),
			c.cfg.Telegram.ChatIDs,
		)

		return &pb.ReportResponse{Success: true, Message: "Report queued successfully"}, nil
	default:
		return &pb.ReportResponse{Success: false, Message: "Queue full"}, nil
	}
}

// GetClusterChan 供 processor 包获取对应集群的 channel
func (c *Central) GetClusterChan(name string) chan *pb.ReportRequest {
	return c.clusterChans[name]
}