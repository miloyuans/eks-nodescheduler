// central/server/http_server.go
package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"

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
		log.Printf("HTTP server starting on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("HTTP server shutting down...")
	ctxShutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctxShutdown); err != nil {
		log.Printf("HTTP shutdown failed: %v", err)
	}
	log.Println("HTTP server shutdown complete")
}