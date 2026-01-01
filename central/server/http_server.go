// central/server/http_server.go
package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"central/config"
	"central/core"
	"central/middleware"
)

func StartHTTP(ctx context.Context, wg *sync.WaitGroup, cfg *config.GlobalConfig, central *core.Central) {
	defer wg.Done()

	whitelist := middleware.New(cfg.Whitelist)
	mux := http.NewServeMux()
	mux.Handle("/report", whitelist.HTTP(http.HandlerFunc(central.HTTPReportHandler)))

	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.HTTP.Addr, cfg.Server.HTTP.Port),
		Handler: mux,
	}

	go func() {
		log.Printf("[INFO] HTTP server starting on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] HTTP server failed: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("[INFO] HTTP server shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("[WARN] HTTP server forced shutdown: %v", err)
	} else {
		log.Println("[INFO] HTTP server shutdown gracefully")
	}
}