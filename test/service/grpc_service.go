package main

import (
	"context"
	"go.opentelemetry.io/otel"
	"log"
	"net"
	"time"

	"github.com/penwyp/mini-gateway/proto/proto"
	"google.golang.org/grpc"
)

type helloServiceServer struct {
	proto.HelloServiceServer
}

func (s *helloServiceServer) GetHello(ctx context.Context, req *proto.HelloRequest) (*proto.HelloResponse, error) {
	// 模拟返回用户信息
	log.Println("GetHello called with name:", req.Name, ",time:", time.Now())
	tracer := otel.Tracer("mini-grpc-service")
	_, span := tracer.Start(ctx, "GetHello")
	defer span.End()
	return &proto.HelloResponse{
		Message: "GetHello " + req.Name,
	}, nil
}

func (s *helloServiceServer) SayHello(ctx context.Context, req *proto.HelloRequest) (*proto.HelloResponse, error) {
	log.Println("SayHello called with name:", req.Name, ",time:", time.Now())
	tracer := otel.Tracer("mini-grpc-service")
	_, span := tracer.Start(ctx, "SayHello")
	defer span.End()
	// 模拟返回用户信息
	return &proto.HelloResponse{
		Message: "SayHello " + req.Name,
	}, nil
}

func (s *helloServiceServer) ReplyHello(ctx context.Context, req *proto.HelloRequest) (*proto.HelloResponse, error) {
	log.Println("ReplyHello called with name:", req.Name, ",time:", time.Now())
	tracer := otel.Tracer("mini-grpc-service")
	_, span := tracer.Start(ctx, "ReplyHello")
	defer span.End()
	// 模拟返回用户信息
	return &proto.HelloResponse{
		Message: "ReplyHello " + req.Name,
	}, nil
}

func main() {
	lis, err := net.Listen("tcp", ":8391")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	s := grpc.NewServer()
	proto.RegisterHelloServiceServer(s, &helloServiceServer{})

	log.Println("gRPC server listening on :8391")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
