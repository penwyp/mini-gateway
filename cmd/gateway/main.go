package main

import (
	"context"
	"fmt"
	"github.com/penwyp/mini-gateway/internal/core/loadbalancer"
	"github.com/penwyp/mini-gateway/plugins"
	"os"
	"os/signal"
	"runtime"
	"sort"
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

	startTime = time.Now() // 记录程序启动时间
	server    *Server      // 全局 Server 实例，便于访问
)

func main() {
	configMgr := config.InitConfig()
	server = initServer(configMgr)

	// 启动配置刷新监听
	go refreshConfig(server, configMgr)

	server.start()
}

// Server 结构体封装服务相关组件
type Server struct {
	Router         *gin.Engine
	ConfigMgr      *config.ConfigManager
	TracingCleanup func(context.Context) error // 追踪清理函数
	LoadBalancer   loadbalancer.LoadBalancer   // 动态更新的负载均衡器
	HTTPProxy      *routing.HTTPProxy
}

// 初始化服务
func initServer(configMgr *config.ConfigManager) *Server {
	cfg := configMgr.GetConfig()
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

	// 初始化健康检查
	health.InitHealthChecker(cfg)

	s := &Server{
		Router:    setupGinRouter(cfg),
		ConfigMgr: configMgr,
	}

	// 初始化 RBAC 和中间件
	if cfg.Security.AuthMode == "rbac" && cfg.Security.RBAC.Enabled {
		security.InitRBAC(cfg)
	}
	s.setupMiddleware(cfg)
	s.setupHTTPProxy(cfg)
	s.setupRoutes(cfg)

	return s
}

// statusHandler 完整实现
func statusHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		logger.Info("Received status check request", zap.String("clientIP", c.ClientIP()))

		// 1. 网关自身状态
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		gatewayStatus := GatewayStatus{
			Uptime:         time.Since(startTime),
			Version:        Version,
			MemoryAlloc:    m.Alloc,
			GoroutineCount: runtime.NumGoroutine(),
		}

		// 2. 后端目标状态
		backendStats := health.GetGlobalHealthChecker().GetAllStats()

		// 3. 负载均衡状态
		lbStatus := server.getLoadBalancerStatus()

		//// 4. 流量治理状态
		//trafficStatus := getTrafficStatus(server.ConfigMgr.GetConfig())

		//// 5. 可观测性指标
		//obsStatus := getObservabilityStatus()

		// 6. 插件状态
		pluginStatus := getPluginStatus()

		// 组合响应
		c.JSON(200, gin.H{
			"status":        "ok",
			"gateway":       gatewayStatus,
			"backend_stats": backendStats,
			"load_balancer": lbStatus,
			//"traffic_status": trafficStatus,
			//"observability":  obsStatus,
			"plugins": pluginStatus,
		})
	}
}

// GatewayStatus 网关自身状态
type GatewayStatus struct {
	Uptime         time.Duration `json:"uptime"`
	Version        string        `json:"version"`
	MemoryAlloc    uint64        `json:"memory_alloc_bytes"`
	GoroutineCount int           `json:"goroutine_count"`
}

// getLoadBalancerStatus 获取负载均衡状态
func (s *Server) getLoadBalancerStatus() map[string]any {
	lbType := s.HTTPProxy.GetLoadBalancerType()
	activeTargets := s.HTTPProxy.GetLoadBalancerActiveTargets()
	unhealthyTargets := s.getUnhealthyTargets()

	return map[string]any{
		"type":              lbType,
		"active_targets":    len(activeTargets),
		"unhealthy_targets": unhealthyTargets,
	}
}

// getUnhealthyTargets 获取不可用目标列表
func (s *Server) getUnhealthyTargets() []string {
	var unhealthy []string
	stats := health.GetGlobalHealthChecker().GetAllStats()
	for _, stat := range stats {
		// 判断条件：探活失败次数大于成功次数
		if stat.ProbeFailureCount > stat.ProbeSuccessCount {
			unhealthy = append(unhealthy, stat.URL)
		}
	}
	return unhealthy
}

//
//// TrafficStatus 流量治理状态
//type TrafficStatus struct {
//	RateLimitEnabled bool              `json:"rate_limit_enabled"`
//	QPS              int               `json:"qps"`
//	Burst            int               `json:"burst"`
//	BreakerStats     map[string]string `json:"breaker_stats"`
//}
//
//// getTrafficStatus 获取流量治理状态
//func getTrafficStatus(cfg *config.Config) TrafficStatus {
//	breakerStats := make(map[string]string)
//	// 遍历路由规则，获取每个目标的熔断状态
//	for path, rules := range cfg.Routing.Rules {
//		for _, rule := range rules {
//			// 假设 traffic 包提供 GetBreakerStatus 方法
//			breakerStats[rule.Target] = traffic.GetBreakerStatus(rule.Target) // 需要在 traffic 包中实现
//		}
//	}
//	return TrafficStatus{
//		RateLimitEnabled: cfg.Middleware.RateLimit,
//		QPS:              cfg.Traffic.RateLimit.QPS,
//		Burst:            cfg.Traffic.RateLimit.Burst,
//		BreakerStats:     breakerStats,
//	}
//}

//// ObservabilityStatus 可观测性指标
//type ObservabilityStatus struct {
//	RequestsTotal        float64 `json:"requests_total"`
//	RequestDurationAvg   float64 `json:"request_duration_avg_seconds"`
//	ActiveWebSocketConns float64 `json:"active_websocket_connections"`
//}
//
//// getObservabilityStatus 获取可观测性指标
//func getObservabilityStatus() ObservabilityStatus {
//	// 假设 observability 包提供 GetRequestsTotal 和 GetRequestDurationAvg 方法
//	return ObservabilityStatus{
//		RequestsTotal:        observability.GetRequestsTotal(),                 // 需要在 observability 包中实现
//		RequestDurationAvg:   observability.GetRequestDurationAvg(),            // 需要在 observability 包中实现
//		ActiveWebSocketConns: observability.ActiveWebSocketConnections.Value(), // 假设是 Gauge
//	}
//}

// PluginStatus 插件状态
type PluginStatus struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

// getPluginStatus 获取插件状态
func getPluginStatus() []PluginStatus {
	var status []PluginStatus
	loadedPlugins := plugins.GetLoadedPlugins()
	for _, p := range loadedPlugins {
		status = append(status, PluginStatus{
			Name:        p.PluginInfo().Name,
			Description: p.PluginInfo().Description,
			Version:     p.PluginInfo().Version.String(),
			Enabled:     true,
		})
	}
	sort.Slice(status, func(i, j int) bool {
		return status[i].Name < status[j].Name
	})
	return status
}

// 配置验证
func validateConfig(cfg *config.Config) {
	if cfg.Routing.LoadBalancer != "consul" && (cfg.Routing.Rules == nil || len(cfg.Routing.Rules) == 0) {
		logger.Error("Routing rules are empty or not defined in configuration")
		os.Exit(1)
	}
}

// 初始化 Gin 路由器
func setupGinRouter(cfg *config.Config) *gin.Engine {
	gin.SetMode(cfg.Server.GinMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestMetricsMiddleware())
	return r
}

// 配置中间件
func (s *Server) setupMiddleware(cfg *config.Config) {
	// 清空现有中间件（仅保留 Recovery 和 Metrics）
	s.Router = setupGinRouter(cfg)

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
		s.TracingCleanup = cleanup
		s.Router.Use(middleware.Tracing())
	}
}

// 配置 HTTP 代理
func (s *Server) setupHTTPProxy(cfg *config.Config) {
	s.HTTPProxy = routing.NewHTTPProxy(cfg)
	logger.Info("HTTP proxy initialized with load balancer", zap.String("type", cfg.Routing.LoadBalancer))
}

// 配置路由
func (s *Server) setupRoutes(cfg *config.Config) {
	// 基本路由
	s.Router.POST("/login", loginHandler(cfg))
	s.Router.GET("/health", healthHandler())
	s.Router.GET("/status", statusHandler())

	// Prometheus 监控
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
	routing.Setup(protected, s.HTTPProxy, cfg)
	logger.Info("Dynamic routing setup completed")
}

// 刷新配置
func refreshConfig(server *Server, configMgr *config.ConfigManager) {
	for newCfg := range configMgr.ConfigChan {
		logger.Info("Refreshing server configuration")

		// 更新中间件
		server.setupMiddleware(newCfg)

		// 更新路由
		server.setupRoutes(newCfg)

		// 刷新负载均衡器
		server.HTTPProxy.RefreshLoadBalancer(newCfg)

		// 刷新健康检查目标
		health.GetGlobalHealthChecker().RefreshTargets(newCfg)

		logger.Info("Server configuration refreshed successfully")
	}
}

// 启动服务
func (s *Server) start() {
	cfg := s.ConfigMgr.GetConfig()
	logStartupInfo(cfg)

	listenAddr := ":" + cfg.Server.Port
	logger.Info("Server starting to listen", zap.String("address", listenAddr))
	go func() {
		if err := s.Router.Run(listenAddr); err != nil {
			logger.Error("Failed to start server", zap.Error(err))
			os.Exit(1)
		}
	}()

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

	if s.TracingCleanup != nil {
		if err := s.TracingCleanup(context.Background()); err != nil {
			logger.Error("Failed to shut down tracer provider", zap.Error(err))
		}
	}
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
