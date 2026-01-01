// central/server/http_server.go
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"central/config"
	"central/middleware"
	pb "central/proto"
)

func StartHTTP(wg *sync.WaitGroup, cfg *config.GlobalConfig, central *Central) {
	defer wg.Done()

	whitelist := middleware.New(cfg.Whitelist)
	mux := http.NewServeMux()
	mux.Handle("/report", whitelist.HTTP(http.HandlerFunc(central.httpReportHandler)))

	addr := fmt.Sprintf("%s:%d", cfg.Server.HTTP.Addr, cfg.Server.HTTP.Port)
	log.Printf("HTTP server starting on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func (central *Central) httpReportHandler(w http.ResponseWriter, r *http.Request) {
	var req pb.ReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if ch, ok := central.clusterChans[req.ClusterName]; ok {
		ch <- &req
		w.Write([]byte(`{"success": true}`))
	} else {
		http.Error(w, "Cluster not found", http.StatusNotFound)
	}
}