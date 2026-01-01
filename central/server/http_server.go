// central/server/http_server.go
package server

import (
	"fmt"
	"log"
	"net/http"
	"sync"

	"central/config"
	"central/core"
	"central/middleware"
)

func StartHTTP(wg *sync.WaitGroup, cfg *config.GlobalConfig, central *core.Central) {
	defer wg.Done()

	whitelist := middleware.New(cfg.Whitelist)
	mux := http.NewServeMux()
	mux.Handle("/report", whitelist.HTTP(http.HandlerFunc(central.HTTPReportHandler)))

	addr := fmt.Sprintf("%s:%d", cfg.Server.HTTP.Addr, cfg.Server.HTTP.Port)
	log.Printf("HTTP server starting on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}