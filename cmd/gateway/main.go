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

var (
	Version   string
	BuildTime string
	GitCommit string
	GoVersion string
)

func main() {
	// 初始化配置
	cfg := config.InitConfig()

	// 初始化日志系统
	logger.Init(logger.Config{
		Level:      cfg.Logger.Level,
		FilePath:   cfg.Logger.FilePath,
		MaxSize:    cfg.Logger.MaxSize,
		MaxBackups: cfg.Logger.MaxBackups,
		MaxAge:     cfg.Logger.MaxAge,
		Compress:   cfg.Logger.Compress,
	})

	// 验证路由配置
	if cfg.Routing.LoadBalancer != "consul" && (cfg.Routing.Rules == nil || len(cfg.Routing.Rules) == 0) {
		logger.Error("Routing rules are empty or not defined in configuration")
		os.Exit(1)
	}

	// 初始化缓存和IP规则
	cache.Init(cfg)
	if cfg.Middleware.IPAcl {
		security.InitIPRules(cfg)
	}

	// 初始化监控和健康检查
	observability.InitMetrics()
	health.NewHealthChecker(cfg)

	// 初始化RBAC（如果启用）
	if cfg.Security.AuthMode == "rbac" && cfg.Security.RBAC.Enabled {
		security.InitRBAC(cfg)
	}

	// 记录启动信息
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

	// 记录中间件状态
	logger.Info("Middleware status",
		zap.Bool("RateLimit", cfg.Middleware.RateLimit),
		zap.Bool("IPAcl", cfg.Middleware.IPAcl),
		zap.Bool("AntiInjection", cfg.Middleware.AntiInjection),
		zap.Bool("Breaker", cfg.Middleware.Breaker),
		zap.Bool("Tracing", cfg.Middleware.Tracing),
	)

	// 初始化Gin路由器
	gin.SetMode(cfg.Server.GinMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestMetricsMiddleware())

	// 装载自定义插件
	plugins.LoadPlugins(r, cfg)

	// 定义漏桶限流器变量
	var leakyLimiter *traffic.LeakyBucketLimiter

	// 配置限流中间件
	if cfg.Middleware.RateLimit {
		switch cfg.Traffic.RateLimit.Algorithm {
		case "token_bucket":
			r.Use(traffic.TokenBucketRateLimit())
		case "leaky_bucket":
			leakyLimiter = traffic.NewLeakyBucketLimiter(cfg.Traffic.RateLimit.QPS, cfg.Traffic.RateLimit.Burst)
			r.Use(traffic.LeakyBucketRateLimit())
		default:
			logger.Error("Unknown rate limiting algorithm", zap.String("algorithm", cfg.Traffic.RateLimit.Algorithm))
			os.Exit(1)
		}
	}

	// 配置其他中间件
	if cfg.Middleware.IPAcl {
		r.Use(security.IPAcl())
	}
	if cfg.Middleware.AntiInjection {
		r.Use(security.AntiInjection())
	}
	if cfg.Middleware.Breaker {
		r.Use(traffic.Breaker())
	}
	if cfg.Middleware.Tracing {
		cleanup := observability.InitTracing(cfg)
		defer func() {
			if err := cleanup(context.Background()); err != nil {
				logger.Error("Failed to shut down tracer provider", zap.Error(err))
			}
		}()
		r.Use(middleware.Tracing())
	}

	// 设置文件服务路由
	fileServerRouter := routing.NewFileServerRouter(cfg)
	fileServerRouter.Setup(r, cfg)

	// 配置基础路由
	r.POST("/login", loginHandler(cfg))
	r.GET("/health", healthHandler())
	r.GET("/status", statusHandler())

	// 配置Prometheus监控端点
	if cfg.Observability.Prometheus.Enabled {
		r.GET(cfg.Observability.Prometheus.Path, gin.WrapH(promhttp.Handler()))
	}

	// 设置动态路由
	logger.Info("Setting up dynamic routing", zap.Any("routing_rules", cfg.Routing.Rules))
	protected := r.Group("/")
	if cfg.Middleware.Auth {
		protected.Use(auth.Auth())
	}
	routing.Setup(protected, cfg)
	logger.Info("Dynamic routing setup completed")

	// 启动服务器
	listenAddr := ":" + cfg.Server.Port
	logger.Info("Server starting to listen", zap.String("address", listenAddr))
	go func() {
		if err := r.Run(listenAddr); err != nil {
			logger.Error("Failed to start server", zap.Error(err))
			os.Exit(1)
		}
	}()

	// 优雅关闭处理
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("Shutting down server...")

	// 关闭健康检查
	health.GetGlobalHealthChecker().Close()

	// 清理漏桶限流器（如果使用）
	if cfg.Middleware.RateLimit && cfg.Traffic.RateLimit.Algorithm == "leaky_bucket" && leakyLimiter != nil {
		traffic.CleanupLeakyBucket(leakyLimiter)
	}

	// 同步日志并退出
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
