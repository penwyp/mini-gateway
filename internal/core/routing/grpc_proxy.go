package routing

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"github.com/penwyp/mini-gateway/proto" // 确保这与你的proto包路径一致
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// SetupGRPCProxy 设置 HTTP 到 gRPC 的反向代理
func SetupGRPCProxy(cfg *config.Config, r *gin.Engine) {
	// 创建 gRPC-Gateway 的多路复用器
	mux := runtime.NewServeMux(
		runtime.WithErrorHandler(runtime.DefaultHTTPErrorHandler),
		runtime.WithForwardResponseOption(httpResponseModifier),
	)

	// 为每个包含 gRPC 协议的路由规则注册服务
	for path, rules := range cfg.Routing.Rules {
		for _, rule := range rules {
			if rule.Protocol == "grpc" {
				// 建立 gRPC 连接
				conn, err := grpc.Dial(
					rule.Target,
					grpc.WithTransportCredentials(insecure.NewCredentials()),
				)
				if err != nil {
					logger.Error("无法连接到 gRPC 服务",
						zap.String("target", rule.Target),
						zap.Error(err))
					continue
				}
				// 这里不需要手动关闭conn，因为它会被mux使用直到程序结束
				// defer conn.Close() // 移除此行

				// 注册 ExampleService 的处理程序
				err = proto.RegisterExampleServiceHandler(context.Background(), mux, conn)
				if err != nil {
					logger.Error("注册 gRPC 服务失败",
						zap.String("target", rule.Target),
						zap.Error(err))
					conn.Close()
					continue
				}

				// 将 gRPC-Gateway 处理程序挂载到 Gin 路由
				r.Any(path+"/*any", gin.WrapH(mux))
				logger.Info("gRPC 代理已设置",
					zap.String("path", path),
					zap.String("target", rule.Target))
			}
		}
	}
}

// httpResponseModifier 修改 HTTP 响应
func httpResponseModifier(ctx context.Context, w http.ResponseWriter, p runtime.ProtoMessage) error {
	w.Header().Set("X-Proxy-Type", "grpc-gateway")
	w.Header().Set("X-Powered-By", "mini-gateway")
	return nil
}
