package routing

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"github.com/penwyp/mini-gateway/proto/proto"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"net/http"
	"strings"

	gproto "google.golang.org/protobuf/proto"
)

// SetupGRPCProxy 设置 HTTP 到 gRPC 的反向代理
func SetupGRPCProxy(cfg *config.Config, r gin.IRouter) {
	mux := runtime.NewServeMux(
		runtime.WithErrorHandler(runtime.DefaultHTTPErrorHandler),
		runtime.WithForwardResponseOption(httpResponseModifier),
	)

	for path, rules := range cfg.Routing.GetGrpcRules() {
		for _, rule := range rules {
			if rule.Protocol != "grpc" {
				continue
			}
			conn, err := grpc.NewClient(
				rule.Target,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
			if err != nil {
				logger.Error("无法连接到 gRPC 服务",
					zap.String("target", rule.Target),
					zap.Error(err))
				continue
			}

			// Register handler once during setup
			err = proto.RegisterHelloServiceHandler(context.Background(), mux, conn)
			if err != nil {
				logger.Error("注册 gRPC 服务失败",
					zap.String("target", rule.Target),
					zap.Error(err))
				conn.Close()
				continue
			}
			logger.Info("gRPC 服务已注册",
				zap.String("path", path),
				zap.String("target", rule.Target))
		}

		// Use a handler that propagates the request context
		r.Any(path, func(c *gin.Context) {
			// Normalize URL to avoid redirects (optional)
			req := c.Request
			if strings.HasSuffix(req.URL.Path, "/") {
				req.URL.Path = strings.TrimSuffix(req.URL.Path, "/")
			}

			// Propagate metadata to the context
			ctx := c.Request.Context()
			md := metadata.Pairs("request-id", c.GetHeader("X-Request-ID")) // Example
			ctx = metadata.NewIncomingContext(ctx, md)
			req = req.WithContext(ctx)

			// Proxy to gRPC gateway
			mux.ServeHTTP(c.Writer, req)
		})
		logger.Info("gRPC 代理已设置", zap.String("path", path))
	}
}

// httpResponseModifier 修改 HTTP 响应
func httpResponseModifier(ctx context.Context, w http.ResponseWriter, p gproto.Message) error {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		logger.Info("Metadata found", zap.Any("metadata", md))
	} else {
		logger.Warn("No metadata in context")
	}
	w.Header().Set("X-Proxy-Type", "grpc-gateway")
	w.Header().Set("X-Powered-By", "mini-gateway")
	return nil
}
