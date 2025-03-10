package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/routing"
	"github.com/penwyp/mini-gateway/internal/core/security"
	"github.com/penwyp/mini-gateway/internal/middleware"
	"github.com/penwyp/mini-gateway/pkg/cache"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

var (
	Version   string
	BuildTime string
	GitCommit string
	GoVersion string
)

func main() {
	cfg := config.InitConfig()

	logger.Init(logger.Config{
		Level:      cfg.Logger.Level,
		FilePath:   cfg.Logger.FilePath,
		MaxSize:    cfg.Logger.MaxSize,
		MaxBackups: cfg.Logger.MaxBackups,
		MaxAge:     cfg.Logger.MaxAge,
		Compress:   cfg.Logger.Compress,
	})

	if cfg.Routing.LoadBalancer != "consul" && (cfg.Routing.Rules == nil || len(cfg.Routing.Rules) == 0) {
		logger.Error("路由规则为空或未在配置中定义")
		os.Exit(1)
	}

	// 初始化 Redis 和 IP 规则
	cache.Init(cfg)
	if cfg.Middleware.IPAcl {
		middleware.InitIPRules(cfg)
	}

	if cfg.Security.AuthMode == "rbac" && cfg.Security.RBAC.Enabled {
		security.InitRBAC(cfg)
	}

	logger.Info("网关启动中",
		zap.String("port", cfg.Server.Port),
		zap.String("version", Version),
		zap.String("buildTime", BuildTime),
		zap.String("gitCommit", GitCommit),
		zap.String("goVersion", GoVersion),
		zap.Any("routingRules", cfg.Routing.Rules),
		zap.String("authMode", cfg.Security.AuthMode),
		zap.Bool("rbacEnabled", cfg.Security.RBAC.Enabled),
	)

	r := gin.Default()

	// 保存漏桶限流器的引用，以便清理
	var leakyLimiter *middleware.LeakyBucketLimiter

	// 根据配置选择限流算法
	if cfg.Middleware.RateLimit {
		switch cfg.Traffic.RateLimit.Algorithm {
		case "token_bucket":
			r.Use(middleware.TokenBucketRateLimit())
		case "leaky_bucket":
			leakyLimiter = middleware.NewLeakyBucketLimiter(cfg.Traffic.RateLimit.QPS, cfg.Traffic.RateLimit.Burst)
			r.Use(middleware.LeakyBucketRateLimit())
		default:
			logger.Error("未知的限流算法", zap.String("algorithm", cfg.Traffic.RateLimit.Algorithm))
			os.Exit(1)
		}
	}

	// 其他中间件
	if cfg.Middleware.IPAcl {
		r.Use(middleware.IPAcl())
	}
	if cfg.Middleware.AntiInjection {
		r.Use(middleware.AntiInjection())
	}
	if cfg.Middleware.Breaker {
		r.Use(middleware.Breaker())
	}

	// 路由设置
	r.POST("/login", func(c *gin.Context) {
		var creds struct {
			Username string `json:"username" binding:"required"`
			Password string `json:"password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&creds); err != nil {
			logger.Warn("无效的登录请求", zap.Error(err))
			c.JSON(400, gin.H{"error": "无效请求"})
			return
		}

		if creds.Username != "admin" || creds.Password != "password" {
			logger.Warn("登录失败", zap.String("username", creds.Username))
			c.JSON(401, gin.H{"error": "无效凭证"})
			return
		}

		if cfg.Security.AuthMode == "jwt" {
			token, err := security.GenerateToken(creds.Username)
			if err != nil {
				logger.Error("生成JWT令牌失败", zap.Error(err))
				c.JSON(500, gin.H{"error": "服务器错误"})
				return
			}
			c.JSON(200, gin.H{"token": token})
		} else if cfg.Security.AuthMode == "rbac" {
			token, err := security.GenerateRBACLoginToken(creds.Username)
			if err != nil {
				logger.Error("生成RBAC令牌失败", zap.Error(err))
				c.JSON(500, gin.H{"error": "服务器错误"})
				return
			}
			c.JSON(200, gin.H{"token": token, "username": creds.Username})
		} else {
			c.JSON(200, gin.H{"message": "登录成功", "username": creds.Username})
		}
	})

	r.GET("/health", func(c *gin.Context) {
		logger.Info("健康检查请求", zap.String("clientIP", c.ClientIP()))
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Prometheus 监控端点
	if cfg.Observability.Prometheus.Enabled {
		r.GET(cfg.Observability.Prometheus.Path, gin.WrapH(promhttp.Handler()))
	}

	// 设置动态路由
	protected := r.Group("/")
	if cfg.Middleware.Auth {
		protected.Use(middleware.Auth())
	}
	routing.Setup(protected, cfg)
	// 为所有动态路由注册一个空处理器，交给具体 Router 处理
	for path := range cfg.Routing.Rules {
		protected.Any(path, func(c *gin.Context) {}) // 空处理器，依赖 TrieRouter 中间件
	}

	logger.Info("开始设置动态路由", zap.Any("routing_rules", cfg.Routing.Rules))
	routing.Setup(r, cfg)
	logger.Info("动态路由设置完成")

	// 启动服务器
	listenAddr := ":" + cfg.Server.Port
	logger.Info("服务器开始监听", zap.String("address", listenAddr))
	if err := r.Run(listenAddr); err != nil {
		logger.Error("启动服务器失败", zap.Error(err))
		os.Exit(1)
	}

	// 优雅关闭（仅在收到信号时执行清理）
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("正在关闭服务器...")

	// 清理漏桶资源（如果使用）
	if cfg.Middleware.RateLimit && cfg.Traffic.RateLimit.Algorithm == "leaky_bucket" && leakyLimiter != nil {
		middleware.CleanupLeakyBucket(leakyLimiter)
	}

	// 同步日志
	if err := logger.Sync(); err != nil {
		logger.Error("日志同步失败", zap.Error(err))
		os.Exit(1)
	}
}
