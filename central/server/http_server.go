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
)

func StartHTTP(wg *sync.WaitGroup, cfg *config.GlobalConfig, central *Central) {  // Central 已定义
	defer wg.Done()

	whitelist := middleware.New(cfg.Whitelist)
	mux := http.NewServeMux()
	mux.Handle("/report", whitelist.HTTP(http.HandlerFunc(central.httpReportHandler)))  // 正确调用

	addr := fmt.Sprintf("%s:%d", cfg.Server.HTTP.Addr, cfg.Server.HTTP.Port)
	log.Printf("HTTP server starting on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}