package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/health"
	"github.com/penwyp/mini-gateway/internal/core/loadbalancer"
	"github.com/penwyp/mini-gateway/internal/core/observability"
	"github.com/penwyp/mini-gateway/internal/core/routing"
	"github.com/penwyp/mini-gateway/internal/core/routing/proxy"
	"github.com/penwyp/mini-gateway/internal/core/security"
	"github.com/penwyp/mini-gateway/internal/core/traffic"
	"github.com/penwyp/mini-gateway/internal/middleware"
	"github.com/penwyp/mini-gateway/internal/middleware/auth"
	"github.com/penwyp/mini-gateway/pkg/cache"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"github.com/penwyp/mini-gateway/plugins"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

var (
	Version   string // 版本号
	BuildTime string // 构建时间
	GitCommit string // Git 提交哈希
	GoVersion string // Go 版本

	startTime = time.Now() // 程序启动时间
	server    *Server      // 全局 Server 实例
)

func main() {
	configMgr := config.InitConfig() // 初始化配置管理器
	server = initServer(configMgr)   // 初始化服务

	go refreshConfig(server, configMgr) // 启动配置刷新监听协程
	server.start()                      // 启动服务
}

// Server 结构体封装服务相关组件
type Server struct {
	Router         *gin.Engine                 // Gin 路由引擎
	ConfigMgr      *config.ConfigManager       // 配置管理器
	TracingCleanup func(context.Context) error // 分布式追踪清理函数
	LoadBalancer   loadbalancer.LoadBalancer   // 负载均衡器
	HTTPProxy      *proxy.HTTPProxy            // HTTP 代理
}

// initServer 初始化服务实例
func initServer(configMgr *config.ConfigManager) *Server {
	cfg := configMgr.GetConfig() // 获取当前配置
	// 初始化日志
	logger.Init(logger.Config{
		Level:      cfg.Logger.Level,
		FilePath:   cfg.Logger.FilePath,
		MaxSize:    cfg.Logger.MaxSize,
		MaxBackups: cfg.Logger.MaxBackups,
		MaxAge:     cfg.Logger.MaxAge,
		Compress:   cfg.Logger.Compress,
	})

	validateConfig(cfg)           // 验证配置有效性
	cache.Init(cfg)               // 初始化缓存
	observability.InitMetrics()   // 初始化监控指标
	health.InitHealthChecker(cfg) // 初始化健康检查

	s := &Server{
		Router:    setupGinRouter(cfg), // 设置 Gin 路由器
		ConfigMgr: configMgr,
	}

	// 如果启用了 RBAC 认证，则初始化 RBAC
	if cfg.Security.AuthMode == "rbac" && cfg.Security.RBAC.Enabled {
		security.InitRBAC(cfg)
	}
	s.setupMiddleware(cfg) // 配置中间件
	s.setupHTTPProxy(cfg)  // 配置 HTTP 代理
	s.setupRoutes(cfg)     // 配置路由

	return s
}

// setupRoutes 配置所有路由，简洁调用独立处理函数
func (s *Server) setupRoutes(cfg *config.Config) {
	// 基本路由
	s.Router.GET("/health", s.handleHealth) // 健康检查路由
	s.Router.GET("/status", s.handleStatus) // 状态检查路由
	s.Router.POST("/login", s.handleLogin)  // 登录路由

	// Prometheus 监控路由
	if cfg.Observability.Prometheus.Enabled {
		s.Router.GET(cfg.Observability.Prometheus.Path, gin.WrapH(promhttp.Handler()))
	}

	// 文件服务路由
	fileServerRouter := routing.NewFileServerRouter(cfg)
	fileServerRouter.Setup(s.Router, cfg)

	// 路由管理 API
	routeGroup := s.Router.Group("/api/routes")
	{
		routeGroup.POST("/add", s.handleAddRoute)         // 添加路由
		routeGroup.PUT("/update", s.handleUpdateRoute)    // 更新路由
		routeGroup.DELETE("/delete", s.handleDeleteRoute) // 删除路由
		routeGroup.GET("/list", s.handleListRoutes)       // 列出所有路由
	}

	// 保存配置 API
	s.Router.POST("/api/config/save", s.handleSaveConfig)

	// 动态路由
	logger.Info("设置动态路由", zap.Any("routing_rules", cfg.Routing.Rules))
	protected := s.Router.Group("/")
	if cfg.Middleware.Auth {
		protected.Use(auth.Auth()) // 应用认证中间件
	}
	routing.Setup(protected, s.HTTPProxy, cfg)
	logger.Info("动态路由设置完成")
}

// handleHealth 处理健康检查请求
func (s *Server) handleHealth(c *gin.Context) {
	logger.Info("收到健康检查请求", zap.String("clientIP", c.ClientIP()))
	c.JSON(200, gin.H{"status": "ok"})
}

// handleStatus 处理状态检查请求
func (s *Server) handleStatus(c *gin.Context) {
	logger.Info("收到状态检查请求", zap.String("clientIP", c.ClientIP()))

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	gatewayStatus := GatewayStatus{
		Uptime:         time.Since(startTime),
		Version:        Version,
		MemoryAlloc:    m.Alloc,
		GoroutineCount: runtime.NumGoroutine(),
	}

	backendStats := health.GetGlobalHealthChecker().GetAllStats()
	lbStatus := s.getLoadBalancerStatus()
	pluginStatus := getPluginStatus()

	c.JSON(200, gin.H{
		"status":        "ok",
		"gateway":       gatewayStatus,
		"backend_stats": backendStats,
		"load_balancer": lbStatus,
		"plugins":       pluginStatus,
	})
}

// handleLogin 处理登录请求
func (s *Server) handleLogin(c *gin.Context) {
	var creds struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&creds); err != nil {
		logger.Warn("无效的登录请求", zap.Error(err))
		c.JSON(400, gin.H{"error": "Invalid request"})
		return
	}

	if creds.Username != "admin" || creds.Password != "password" {
		logger.Warn("登录失败", zap.String("username", creds.Username))
		c.JSON(401, gin.H{"error": "Invalid credentials"})
		return
	}

	cfg := s.ConfigMgr.GetConfig()
	switch cfg.Security.AuthMode {
	case "jwt":
		token, err := security.GenerateToken(creds.Username)
		if err != nil {
			logger.Error("生成 JWT token 失败", zap.Error(err))
			c.JSON(500, gin.H{"error": "Server error"})
			return
		}
		c.JSON(200, gin.H{"token": token})
	case "rbac":
		token, err := security.GenerateRBACLoginToken(creds.Username)
		if err != nil {
			logger.Error("生成 RBAC token 失败", zap.Error(err))
			c.JSON(500, gin.H{"error": "Server error"})
			return
		}
		c.JSON(200, gin.H{"token": token, "username": creds.Username})
	default:
		c.JSON(200, gin.H{"message": "Login successful", "username": creds.Username})
	}
}

// handleAddRoute 处理添加路由请求
func (s *Server) handleAddRoute(c *gin.Context) {
	var route struct {
		Path  string              `json:"path" binding:"required"`
		Rules config.RoutingRules `json:"rules" binding:"required"`
	}
	if err := c.ShouldBindJSON(&route); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request payload"})
		return
	}

	cfg := s.ConfigMgr.GetConfig()
	if cfg.Routing.Rules == nil {
		cfg.Routing.Rules = make(map[string]config.RoutingRules)
	}

	if _, exists := cfg.Routing.Rules[route.Path]; exists {
		c.JSON(409, gin.H{"error": "Route already exists"})
		return
	}

	cfg.Routing.Rules[route.Path] = route.Rules
	s.ConfigMgr.UpdateConfig(cfg)
	logger.Info("路由已添加", zap.String("path", route.Path), zap.Any("rules", route.Rules))
	c.JSON(200, gin.H{"message": "Route added successfully"})
}

// handleUpdateRoute 处理更新路由请求
func (s *Server) handleUpdateRoute(c *gin.Context) {
	var route struct {
		Path  string              `json:"path" binding:"required"`
		Rules config.RoutingRules `json:"rules" binding:"required"`
	}
	if err := c.ShouldBindJSON(&route); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request payload"})
		return
	}

	path, rules := route.Path, route.Rules

	cfg := s.ConfigMgr.GetConfig()
	if _, exists := cfg.Routing.Rules[path]; !exists {
		c.JSON(404, gin.H{"error": "Route not found"})
		return
	}

	cfg.Routing.Rules[path] = rules
	s.ConfigMgr.UpdateConfig(cfg)
	logger.Info("路由已更新", zap.String("path", path), zap.Any("rules", rules))
	c.JSON(200, gin.H{"message": "Route updated successfully"})
}

// handleDeleteRoute 处理删除路由请求
func (s *Server) handleDeleteRoute(c *gin.Context) {
	var route struct {
		Path  string              `json:"path" binding:"required"`
		Rules config.RoutingRules `json:"rules" binding:"required"`
	}
	if err := c.ShouldBindJSON(&route); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request payload"})
		return
	}

	path := route.Path
	cfg := s.ConfigMgr.GetConfig()

	if _, exists := cfg.Routing.Rules[path]; !exists {
		c.JSON(404, gin.H{"error": "Route not found"})
		return
	}

	delete(cfg.Routing.Rules, path)
	s.ConfigMgr.UpdateConfig(cfg)
	logger.Info("路由已删除", zap.String("path", path))
	c.JSON(200, gin.H{"message": "Route deleted successfully"})
}

// handleListRoutes 处理列出所有路由请求
func (s *Server) handleListRoutes(c *gin.Context) {
	cfg := s.ConfigMgr.GetConfig()
	c.JSON(200, gin.H{"routes": cfg.Routing.Rules})
}

// handleSaveConfig 处理保存配置请求
func (s *Server) handleSaveConfig(c *gin.Context) {
	cfg := s.ConfigMgr.GetConfig()
	err := s.ConfigMgr.SaveConfigToFile(cfg, "./config/config.yaml")
	if err != nil {
		logger.Error("保存配置失败", zap.Error(err))
		c.JSON(500, gin.H{"error": "Failed to save configuration"})
		return
	}
	logger.Info("配置已保存到文件")
	c.JSON(200, gin.H{"message": "Configuration saved successfully"})
}

// setupMiddleware 配置中间件
func (s *Server) setupMiddleware(cfg *config.Config) {
	s.Router = setupGinRouter(cfg)

	if cfg.Caching.Enabled {
		s.Router.Use(middleware.CacheMiddleware(cfg)) // 启用缓存中间件
	}

	plugins.LoadPlugins(s.Router, cfg) // 加载自定义插件

	if cfg.Middleware.IPAcl {
		security.InitIPRules(cfg)
		s.Router.Use(security.IPAcl()) // IP 访问控制
	}
	if cfg.Middleware.AntiInjection {
		s.Router.Use(security.AntiInjection()) // 防注入攻击
	}

	if cfg.Middleware.RateLimit {
		switch cfg.Traffic.RateLimit.Algorithm {
		case "token_bucket":
			s.Router.Use(traffic.TokenBucketRateLimit()) // 令牌桶限流
		case "leaky_bucket":
			s.Router.Use(traffic.LeakyBucketRateLimit()) // 漏桶限流
		default:
			logger.Error("未知的限流算法", zap.String("algorithm", cfg.Traffic.RateLimit.Algorithm))
			os.Exit(1)
		}
	}
	if cfg.Middleware.Breaker {
		s.Router.Use(traffic.Breaker()) // 熔断器
	}

	if cfg.Middleware.Tracing {
		cleanup := observability.InitTracing(cfg)
		s.TracingCleanup = cleanup
		s.Router.Use(middleware.Tracing()) // 分布式追踪
	}
}

// setupHTTPProxy 配置 HTTP 代理
func (s *Server) setupHTTPProxy(cfg *config.Config) {
	s.HTTPProxy = proxy.NewHTTPProxy(cfg)
	logger.Info("HTTP 代理已初始化，负载均衡类型", zap.String("type", cfg.Routing.LoadBalancer))
}

// refreshConfig 刷新配置
func refreshConfig(server *Server, configMgr *config.ConfigManager) {
	for newCfg := range configMgr.ConfigChan {
		logger.Info("正在刷新服务配置")
		server.setupMiddleware(newCfg)
		server.setupRoutes(newCfg)
		server.HTTPProxy.RefreshLoadBalancer(newCfg)
		health.GetGlobalHealthChecker().RefreshTargets(newCfg)
		logger.Info("服务配置刷新成功")
	}
}

// start 启动服务
func (s *Server) start() {
	cfg := s.ConfigMgr.GetConfig()
	logStartupInfo(cfg)

	listenAddr := ":" + cfg.Server.Port
	logger.Info("服务开始监听", zap.String("address", listenAddr))
	go func() {
		if err := s.Router.Run(listenAddr); err != nil {
			logger.Error("启动服务失败", zap.Error(err))
			os.Exit(1)
		}
	}()

	s.gracefulShutdown()
}

// logStartupInfo 记录服务启动信息
func logStartupInfo(cfg *config.Config) {
	logger.Info("启动 mini-gateway",
		zap.String("port", cfg.Server.Port),
		zap.String("version", Version),
		zap.String("buildTime", BuildTime),
		zap.String("gitCommit", GitCommit),
		zap.String("goVersion", GoVersion),
		zap.Any("routingRules", cfg.Routing.Rules),
		zap.String("authMode", cfg.Security.AuthMode),
		zap.Bool("rbacEnabled", cfg.Security.RBAC.Enabled),
	)

	logger.Info("中间件状态",
		zap.Bool("RateLimit", cfg.Middleware.RateLimit),
		zap.Bool("IPAcl", cfg.Middleware.IPAcl),
		zap.Bool("AntiInjection", cfg.Middleware.AntiInjection),
		zap.Bool("Breaker", cfg.Middleware.Breaker),
		zap.Bool("Tracing", cfg.Middleware.Tracing),
	)
}

// gracefulShutdown 优雅关闭服务
func (s *Server) gracefulShutdown() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("正在关闭服务...")

	if s.TracingCleanup != nil {
		if err := s.TracingCleanup(context.Background()); err != nil {
			logger.Error("关闭追踪提供者失败", zap.Error(err))
		}
	}
	health.GetGlobalHealthChecker().Close()
	if err := logger.Sync(); err != nil {
		logger.Error("同步日志失败", zap.Error(err))
		os.Exit(1)
	}
}

// setupGinRouter 初始化 Gin 路由器
func setupGinRouter(cfg *config.Config) *gin.Engine {
	gin.SetMode(cfg.Server.GinMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestMetricsMiddleware())
	return r
}

// validateConfig 验证配置
func validateConfig(cfg *config.Config) {
	if cfg.Routing.LoadBalancer != "consul" && (cfg.Routing.Rules == nil || len(cfg.Routing.Rules) == 0) {
		logger.Error("路由规则为空或未定义")
		os.Exit(1)
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
		if stat.ProbeFailureCount > stat.ProbeSuccessCount {
			unhealthy = append(unhealthy, stat.URL)
		}
	}
	return unhealthy
}

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
