package routing

import (
	"context"
	"fmt"
	"github.com/penwyp/mini-gateway/internal/core/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"github.com/penwyp/mini-gateway/proto/proto"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	gproto "google.golang.org/protobuf/proto"
)

var grpcTracer = otel.Tracer("proxy:http")

// SetupGRPCProxy 设置 HTTP 到 gRPC 的反向代理
func SetupGRPCProxy(cfg *config.Config, r gin.IRouter) {
	mux := runtime.NewServeMux(
		runtime.WithErrorHandler(httpErrorHandler()),
		runtime.WithForwardResponseOption(httpResponseModifier),
	)

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),     // 本地测试用，生产环境需配置 TLS
		grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()), // 添加 gRPC 客户端拦截器
	}

	for path, rules := range cfg.Routing.GetGrpcRules() {
		for _, rule := range rules {
			if rule.Protocol != "grpc" {
				continue
			}
			conn, err := grpc.NewClient(
				rule.Target,
				opts...,
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
			ctx, span := grpcTracer.Start(c.Request.Context(), "GRPCProxy.Handle",
				trace.WithAttributes(
					attribute.String("http.method", c.Request.Method),
					attribute.String("http.path", c.Request.URL.Path),
					attribute.String("grpc.prefix", cfg.GRPC.Prefix),
				))
			defer span.End()

			span.SetAttributes(attribute.String("grpc.routing.path", c.Request.URL.Path))

			// Normalize URL to avoid redirects (optional)
			req := c.Request
			if strings.HasSuffix(req.URL.Path, "/") {
				req.URL.Path = strings.TrimSuffix(req.URL.Path, "/")
			}

			originalPath := c.Request.URL.Path
			grpcPrefix := cfg.GRPC.Prefix
			adjustedPath := strings.TrimPrefix(originalPath, grpcPrefix)
			if adjustedPath == originalPath {
				logger.Warn("路径未包含 gRPC 前缀，无需调整",
					zap.String("path", originalPath),
					zap.String("prefix", grpcPrefix))
			} else {
				logger.Info("调整 gRPC 路径，移除前缀",
					zap.String("original_path", originalPath),
					zap.String("adjusted_path", adjustedPath),
					zap.String("prefix", grpcPrefix))
				c.Request.URL.Path = adjustedPath // 修改请求路径
			}

			// Propagate metadata to the context
			ctx = c.Request.Context()
			md := metadata.Pairs("request-id", c.GetHeader("X-Request-ID")) // Example
			ctx = metadata.NewIncomingContext(ctx, md)
			req = req.WithContext(ctx)

			start := time.Now()

			// Proxy to gRPC gateway
			mux.ServeHTTP(c.Writer, req)

			// 记录请求延迟（可选）
			duration := time.Since(start).Seconds()
			observability.RequestDuration.WithLabelValues(c.Request.Method, c.Request.URL.Path).Observe(duration)
			span.SetStatus(codes.Ok, "gRPC proxy successful")
		})
		logger.Info("gRPC 代理已设置", zap.String("path", path))
	}
}

func httpErrorHandler() func(ctx context.Context, mux *runtime.ServeMux, marshaler runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
	return func(ctx context.Context, mux *runtime.ServeMux, marshaler runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
		st, _ := status.FromError(err)
		statusCode := fmt.Sprintf("%d", st.Code())
		path := r.URL.Path

		// 记录失败的 gRPC 请求
		observability.GRPCCallsTotal.WithLabelValues(path, statusCode).Inc()
		logger.Error("gRPC 请求失败",
			zap.String("path", path),
			zap.String("status", statusCode),
			zap.String("error", st.Message()))

		// 默认错误处理
		runtime.DefaultHTTPErrorHandler(ctx, mux, marshaler, w, r, err)
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
