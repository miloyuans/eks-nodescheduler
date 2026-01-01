// central/server/grpc_server.go
package server

import (
	"fmt"
	"log"
	"net"

	"central/config"
	"central/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "central/proto"  // proto 已生成
)

func StartGRPC(wg *sync.WaitGroup, cfg *config.GlobalConfig, central *Central) {  // Central 已定义
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
	pb.RegisterAutoscalerServiceServer(grpcServer, central)  // 正确注册
	reflection.Register(grpcServer)

	log.Printf("gRPC server starting on %s", addr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("gRPC server failed: %v", err)
	}
}