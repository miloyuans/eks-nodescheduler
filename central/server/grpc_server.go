// central/server/grpc_server.go
package server

import (
	"log"
	"net"
	"sync"

	"central/config"
	"google.golang.org/grpc"
)

func StartGRPC(wg *sync.WaitGroup, cfg *config.GlobalConfig, central *Central) {
	defer wg.Done()

	addr := cfg.Server.GRPC.Addr + ":" + fmt.Sprintf("%d", cfg.Server.GRPC.Port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("gRPC listen failed: %v", err)
	}

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(central.grpcWhitelistInterceptor),
	)
	pb.RegisterAutoscalerServiceServer(grpcServer, central)
	reflection.Register(grpcServer)

	log.Printf("gRPC server starting on %s", addr)
	log.Fatal(grpcServer.Serve(lis))
}