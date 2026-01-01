// central/server/grpc_server.go
package server

import (
	"fmt"
	"log"
	"net"
	"sync"

	"central/config"
	"central/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	pb "central/proto"
)

func StartGRPC(wg *sync.WaitGroup, cfg *config.GlobalConfig, central *Central) {
	defer wg.Done()

	whitelist := middleware.New(cfg.Whitelist)
	addr := fmt.Sprintf("%s:%d", cfg.Server.GRPC.Addr, cfg.Server.GRPC.Port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("gRPC listen failed: %v", err)
	}
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(whitelist.GRPCUnary),
	)
	pb.RegisterAutoscalerServiceServer(grpcServer, central)
	reflection.Register(grpcServer)

	log.Printf("gRPC server starting on %s", addr)
	log.Fatal(grpcServer.Serve(lis))
}