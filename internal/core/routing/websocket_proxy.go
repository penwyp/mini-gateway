package routing

import (
	"context"
	"github.com/penwyp/mini-gateway/internal/core/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/loadbalancer"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

var websocketTracer = otel.Tracer("proxy:websocket")

// WebSocketProxy 设置 WebSocket 代理
type WebSocketProxy struct {
	pool *WebSocketPool
	lb   loadbalancer.LoadBalancer
}

// NewWebSocketProxy 创建 WebSocket 代理实例
func NewWebSocketProxy(cfg *config.Config) *WebSocketProxy {
	lb, err := loadbalancer.NewLoadBalancer(cfg.Routing.LoadBalancer, cfg)
	if err != nil {
		logger.Error("创建负载均衡器失败", zap.Error(err))
		lb = loadbalancer.NewRoundRobin()
	}
	return &WebSocketProxy{
		pool: NewWebSocketPool(cfg),
		lb:   lb,
	}
}

// SetupWebSocketProxy 设置 WebSocket 代理路由
func (wp *WebSocketProxy) SetupWebSocketProxy(r gin.IRouter, cfg *config.Config) {
	rules := cfg.Routing.GetWebSocketRules()
	if len(rules) == 0 {
		logger.Info("未找到 WebSocket 路由规则")
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true // 可根据配置调整跨域策略
		},
	}

	for path, targetRules := range rules {
		logger.Info("设置 WebSocket 代理",
			zap.String("path", path),
			zap.Any("targets", targetRules))
		r.GET(path, wp.createWebSocketHandler(targetRules, upgrader, cfg))
	}
}

// createWebSocketHandler 创建 WebSocket 处理函数
func (wp *WebSocketProxy) createWebSocketHandler(rules config.RoutingRules, upgrader websocket.Upgrader, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从 HTTP 请求中提取追踪上下文
		ctx := otel.GetTextMapPropagator().Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))
		ctx, connectSpan := websocketTracer.Start(ctx, "WebSocket.Connect",
			trace.WithAttributes(attribute.String("path", c.Request.URL.Path)))
		defer connectSpan.End()

		// 升级客户端连接为 WebSocket
		clientConn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			connectSpan.RecordError(err)
			connectSpan.SetStatus(codes.Error, "Upgrade failed")
			logger.Error("WebSocket 升级失败",
				zap.String("path", c.Request.URL.Path),
				zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "WebSocket 升级失败"})
			return
		}
		defer clientConn.Close()

		// 增加活跃连接数
		observability.ActiveWebSocketConnections.Inc()
		defer observability.ActiveWebSocketConnections.Dec() // 连接关闭时减少

		// 选择目标（支持负载均衡）
		targets := make([]string, len(rules))
		for i, rule := range rules {
			targets[i] = rule.Target
		}
		target := wp.lb.SelectTarget(targets, c.Request)
		if target == "" {
			connectSpan.SetStatus(codes.Error, "No target available")
			logger.Warn("无可用 WebSocket 目标",
				zap.String("path", c.Request.URL.Path))
			clientConn.WriteMessage(websocket.TextMessage, []byte("无可用目标"))
			return
		}
		logger.Debug("负载均衡选择的目标", zap.String("target", target))

		// 验证 target 是否为有效的 WebSocket URL
		targetURL, err := url.Parse(target)
		if err != nil || (targetURL.Scheme != "ws" && targetURL.Scheme != "wss") {
			connectSpan.RecordError(err)
			connectSpan.SetStatus(codes.Error, "Backend connect failed")
			logger.Error("无效的 WebSocket 目标 URL",
				zap.String("target", target),
				zap.Error(err))
			clientConn.WriteMessage(websocket.TextMessage, []byte("无效的目标地址"))
			return
		}

		// 获取请求路径并移除配置中的 WebSocket 前缀
		originalPath := c.Request.URL.Path
		wsPrefix := cfg.WebSocket.Prefix
		adjustedPath := strings.TrimPrefix(originalPath, wsPrefix)
		if adjustedPath == originalPath {
			logger.Warn("路径未包含 WebSocket 前缀，无需调整",
				zap.String("path", originalPath),
				zap.String("prefix", wsPrefix))
		} else {
			logger.Info("调整 WebSocket 路径，移除前缀",
				zap.String("original_path", originalPath),
				zap.String("adjusted_path", adjustedPath),
				zap.String("prefix", wsPrefix))
		}

		// 拼接完整目标路径
		fullTarget := target
		if adjustedPath != "" && adjustedPath != "/" {
			// 使用 url.URL 处理路径，确保格式正确
			targetURL.Path = path.Join(targetURL.Path, adjustedPath)
			fullTarget = targetURL.String()
		}
		logger.Debug("最终转发目标", zap.String("fullTarget", fullTarget))

		// 从连接池获取或创建后端连接
		backendConn, err := wp.pool.GetConn(fullTarget)
		if err != nil {
			connectSpan.RecordError(err)
			connectSpan.SetStatus(codes.Error, "Backend connect failed")
			logger.Error("获取后端 WebSocket 连接失败",
				zap.String("fullTarget", fullTarget),
				zap.Error(err))
			clientConn.WriteMessage(websocket.TextMessage, []byte("后端连接失败"))
			return
		}

		// 双向消息透传
		errCh := make(chan error, 2)
		go wp.forwardMessages(ctx, clientConn, backendConn, "client-to-backend", errCh)
		go wp.forwardMessages(ctx, backendConn, clientConn, "backend-to-client", errCh)

		if err := <-errCh; err != nil {
			connectSpan.RecordError(err)
			connectSpan.SetStatus(codes.Error, "Message forwarding failed")
			logger.Error("WebSocket 消息透传失败",
				zap.String("path", c.Request.URL.Path),
				zap.String("fullTarget", fullTarget),
				zap.Error(err))
		}
	}
}

func (wp *WebSocketProxy) forwardMessages(ctx context.Context, from, to *websocket.Conn, direction string, errCh chan<- error) {
	for {
		_, span := websocketTracer.Start(ctx, "WebSocket.Message",
			trace.WithAttributes(attribute.String("direction", direction)))
		msgType, msg, err := from.ReadMessage()
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "Read failed")
			span.End()
			errCh <- err
			return
		}
		err = to.WriteMessage(msgType, msg)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "Write failed")
			span.End()
			errCh <- err
			return
		}
		span.SetStatus(codes.Ok, "Message forwarded")
		span.End()
	}
}

// Close 关闭 WebSocket 代理并清理资源
func (wp *WebSocketProxy) Close() {
	wp.pool.Close()
}
