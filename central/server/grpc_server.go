// central/server/grpc_server.go
package server

import (
	"fmt"
	"log"
	"net"
	"sync"

	"central/config"
	"central/core"
	"central/middleware"
	pb "central/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func StartGRPC(wg *sync.WaitGroup, cfg *config.GlobalConfig, central *core.Central) {
	defer wg.Done()

	whitelist := middleware.New(cfg.Whitelist)
	addr := fmt.Sprintf("%s:%d", cfg.Server.GRPC.Addr, cfg.Server.GRPC.Port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("gRPC listen failed: %v", err)
	}

	s := grpc.NewServer(grpc.UnaryInterceptor(whitelist.GRPC))
	pb.RegisterAutoscalerServiceServer(s, central)
	reflection.Register(s)

	log.Printf("gRPC server starting on %s", addr)
	log.Fatal(s.Serve(lis))
}