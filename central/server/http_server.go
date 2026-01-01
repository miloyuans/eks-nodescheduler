// central/server/http_server.go
package server

import (
	"log"
	"net/http"
	"sync"

	"central/config"
	"central/middleware"
)

func StartHTTP(wg *sync.WaitGroup, cfg *config.GlobalConfig, central *Central) {
	defer wg.Done()

	whitelist := middleware.NewWhitelist(cfg.Whitelist)
	mux := http.NewServeMux()
	mux.Handle("/report", whitelist.HTTPMiddleware(http.HandlerFunc(central.httpReportHandler)))

	addr := cfg.Server.HTTP.Addr + ":" + fmt.Sprintf("%d", cfg.Server.HTTP.Port)
	log.Printf("HTTP server starting on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}