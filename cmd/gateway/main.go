package main

import (
	"context"
	"fmt"
	"github.com/penwyp/mini-gateway/plugins"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/health"
	"github.com/penwyp/mini-gateway/internal/core/observability"
	"github.com/penwyp/mini-gateway/internal/core/routing"
	"github.com/penwyp/mini-gateway/internal/core/security"
	"github.com/penwyp/mini-gateway/internal/core/traffic"
	"github.com/penwyp/mini-gateway/internal/middleware"
	"github.com/penwyp/mini-gateway/internal/middleware/auth"
	"github.com/penwyp/mini-gateway/pkg/cache"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.uber.org/zap"
)

// 全局变量保持不变
var (
	Version   string
	BuildTime string
	GitCommit string
	GoVersion string
)

func main() {
	server := initServer()

	// 初始化RBAC
	if server.Config.Security.AuthMode == "rbac" && server.Config.Security.RBAC.Enabled {
		security.InitRBAC(server.Config)
	}

	server.setupMiddleware()
	server.setupRoutes()
	server.start()
}

// Server 结构体封装服务相关组件
type Server struct {
	Router *gin.Engine
	Config *config.Config
}

// 初始化服务
func initServer() *Server {
	cfg := config.InitConfig()

	// 初始化日志
	logger.Init(logger.Config{
		Level:      cfg.Logger.Level,
		FilePath:   cfg.Logger.FilePath,
		MaxSize:    cfg.Logger.MaxSize,
		MaxBackups: cfg.Logger.MaxBackups,
		MaxAge:     cfg.Logger.MaxAge,
		Compress:   cfg.Logger.Compress,
	})

	// 验证配置
	validateConfig(cfg)

	// 初始化核心组件
	cache.Init(cfg)
	observability.InitMetrics()
	health.NewHealthChecker(cfg)

	return &Server{
		Router: setupGinRouter(cfg),
		Config: cfg,
	}
}

// 配置验证
func validateConfig(cfg *config.Config) {
	if cfg.Routing.LoadBalancer != "consul" && (cfg.Routing.Rules == nil || len(cfg.Routing.Rules) == 0) {
		logger.Error("Routing rules are empty or not defined in configuration")
		os.Exit(1)
	}
}

// 初始化Gin路由器
func setupGinRouter(cfg *config.Config) *gin.Engine {
	gin.SetMode(cfg.Server.GinMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestMetricsMiddleware())
	return r
}

// 配置中间件
func (s *Server) setupMiddleware() {
	cfg := s.Config

	// 加载自定义插件
	plugins.LoadPlugins(s.Router, cfg)

	// 配置安全相关中间件
	if cfg.Middleware.IPAcl {
		security.InitIPRules(cfg)
		s.Router.Use(security.IPAcl())
	}
	if cfg.Middleware.AntiInjection {
		s.Router.Use(security.AntiInjection())
	}

	// 配置流量控制中间件
	if cfg.Middleware.RateLimit {
		switch cfg.Traffic.RateLimit.Algorithm {
		case "token_bucket":
			s.Router.Use(traffic.TokenBucketRateLimit())
		case "leaky_bucket":
			s.Router.Use(traffic.LeakyBucketRateLimit())
		default:
			logger.Error("Unknown rate limiting algorithm", zap.String("algorithm", cfg.Traffic.RateLimit.Algorithm))
			os.Exit(1)
		}
	}
	if cfg.Middleware.Breaker {
		s.Router.Use(traffic.Breaker())
	}

	// 配置追踪
	if cfg.Middleware.Tracing {
		cleanup := observability.InitTracing(cfg)
		defer func() {
			if err := cleanup(context.Background()); err != nil {
				logger.Error("Failed to shut down tracer provider", zap.Error(err))
			}
		}()
		s.Router.Use(middleware.Tracing())
	}
}

// 配置路由
func (s *Server) setupRoutes() {
	cfg := s.Config

	// 基本路由
	s.Router.POST("/login", loginHandler(cfg))
	s.Router.GET("/health", healthHandler())
	s.Router.GET("/status", statusHandler())

	// Prometheus监控
	if cfg.Observability.Prometheus.Enabled {
		s.Router.GET(cfg.Observability.Prometheus.Path, gin.WrapH(promhttp.Handler()))
	}

	// 文件服务路由
	fileServerRouter := routing.NewFileServerRouter(cfg)
	fileServerRouter.Setup(s.Router, cfg)

	// 动态路由
	logger.Info("Setting up dynamic routing", zap.Any("routing_rules", cfg.Routing.Rules))
	protected := s.Router.Group("/")
	if cfg.Middleware.Auth {
		protected.Use(auth.Auth())
	}
	routing.Setup(protected, cfg)
	logger.Info("Dynamic routing setup completed")
}

// 启动服务
func (s *Server) start() {
	// 记录启动信息
	logStartupInfo(s.Config)

	// 启动服务器
	listenAddr := ":" + s.Config.Server.Port
	logger.Info("Server starting to listen", zap.String("address", listenAddr))
	go func() {
		if err := s.Router.Run(listenAddr); err != nil {
			logger.Error("Failed to start server", zap.Error(err))
			os.Exit(1)
		}
	}()

	// 优雅关闭
	s.gracefulShutdown()
}

// 记录启动信息
func logStartupInfo(cfg *config.Config) {
	logger.Info("Starting mini-gateway",
		zap.String("port", cfg.Server.Port),
		zap.String("version", Version),
		zap.String("buildTime", BuildTime),
		zap.String("gitCommit", GitCommit),
		zap.String("goVersion", GoVersion),
		zap.Any("routingRules", cfg.Routing.Rules),
		zap.String("authMode", cfg.Security.AuthMode),
		zap.Bool("rbacEnabled", cfg.Security.RBAC.Enabled),
	)

	logger.Info("Middleware status",
		zap.Bool("RateLimit", cfg.Middleware.RateLimit),
		zap.Bool("IPAcl", cfg.Middleware.IPAcl),
		zap.Bool("AntiInjection", cfg.Middleware.AntiInjection),
		zap.Bool("Breaker", cfg.Middleware.Breaker),
		zap.Bool("Tracing", cfg.Middleware.Tracing),
	)
}

// 优雅关闭
func (s *Server) gracefulShutdown() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("Shutting down server...")

	health.GetGlobalHealthChecker().Close()
	if err := logger.Sync(); err != nil {
		logger.Error("Failed to sync logs", zap.Error(err))
		os.Exit(1)
	}
}

// healthHandler 处理健康检查请求
func healthHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		logger.Info("Received health check request", zap.String("clientIP", c.ClientIP()))
		c.JSON(200, gin.H{"status": "ok"})
	}
}

// statusHandler 处理状态检查请求
func statusHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		logger.Info("Received status check request", zap.String("clientIP", c.ClientIP()))
		stats := health.GetGlobalHealthChecker().GetAllStats()
		c.JSON(200, gin.H{
			"status": "ok",
			"data":   stats,
		})
	}
}

// loginHandler 处理登录请求
func loginHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var creds struct {
			Username string `json:"username" binding:"required"`
			Password string `json:"password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&creds); err != nil {
			logger.Warn("Invalid login request", zap.Error(err))
			c.JSON(400, gin.H{"error": "Invalid request"})
			return
		}

		if creds.Username != "admin" || creds.Password != "password" {
			logger.Warn("Login failed", zap.String("username", creds.Username))
			c.JSON(401, gin.H{"error": "Invalid credentials"})
			return
		}

		switch cfg.Security.AuthMode {
		case "jwt":
			token, err := security.GenerateToken(creds.Username)
			if err != nil {
				logger.Error("Failed to generate JWT token", zap.Error(err))
				c.JSON(500, gin.H{"error": "Server error"})
				return
			}
			c.JSON(200, gin.H{"token": token})
		case "rbac":
			token, err := security.GenerateRBACLoginToken(creds.Username)
			if err != nil {
				logger.Error("Failed to generate RBAC token", zap.Error(err))
				c.JSON(500, gin.H{"error": "Server error"})
				return
			}
			c.JSON(200, gin.H{"token": token, "username": creds.Username})
		default:
			c.JSON(200, gin.H{"message": "Login successful", "username": creds.Username})
		}
	}
}

// requestMetricsMiddleware 全局请求监控中间件
func requestMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		method := c.Request.Method
		path := c.Request.URL.Path

		c.Next()

		status := fmt.Sprintf("%d", c.Writer.Status())
		observability.RequestsTotal.WithLabelValues(method, path, status).Inc()
		duration := time.Since(start).Seconds()
		observability.RequestDuration.WithLabelValues(method, path).Observe(duration)
	}
}

// initTracer 初始化分布式追踪
func initTracer(cfg *config.Config) func(context.Context) error {
	exporter, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpoint(cfg.Observability.Jaeger.Endpoint),
		otlptracehttp.WithURLPath("/v1/traces"),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		logger.Error("Failed to create OTLP exporter", zap.Error(err))
		os.Exit(1)
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String("mini-gateway"),
			semconv.ServiceVersionKey.String(Version),
		),
	)
	if err != nil {
		logger.Error("Failed to create resource", zap.Error(err))
		os.Exit(1)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func(ctx context.Context) error {
		logger.Info("Shutting down tracer provider...")
		return tp.Shutdown(ctx)
	}
}
