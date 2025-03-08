package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/routing"
	"github.com/penwyp/mini-gateway/internal/core/security"
	"github.com/penwyp/mini-gateway/internal/middleware"
	"github.com/penwyp/mini-gateway/pkg/logger"
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

	if cfg.Routing.LoadBalancer != "consul" && len(cfg.Routing.Rules) == 0 {
		logger.Error("Routing rules are empty or not defined in configuration")
		os.Exit(1)
	}

	if cfg.Security.AuthMode == "rbac" && cfg.Security.RBAC.Enabled {
		security.InitRBAC(cfg)
	}

	logger.Info("Gateway starting",
		zap.String("port", cfg.Server.Port),
		zap.String("configPath", "config/config.yaml"),
		zap.String("version", Version),
		zap.String("buildTime", BuildTime),
		zap.String("gitCommit", GitCommit),
		zap.String("goVersion", GoVersion),
		zap.Any("routingRules", cfg.Routing.Rules),
		zap.Bool("jwtEnabled", cfg.Security.JWT.Enabled),
	)

	r := gin.Default()
	r.Use(middleware.RateLimit()) // 全局只保留速率限制

	// 无认证路由组
	r.POST("/login", func(c *gin.Context) {
		var creds struct {
			Username string `json:"username" binding:"required"`
			Password string `json:"password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&creds); err != nil {
			logger.Warn("Invalid login request", zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
			return
		}

		if creds.Username != "admin" || creds.Password != "password" {
			logger.Warn("Login failed",
				zap.String("username", creds.Username),
			)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
			return
		}

		if cfg.Security.AuthMode == "jwt" {
			token, err := security.GenerateToken(creds.Username)
			if err != nil {
				logger.Error("Failed to generate JWT token", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Server error"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"token": token})
		} else if cfg.Security.AuthMode == "rbac" {
			token, err := security.GenerateRBACLoginToken(creds.Username)
			if err != nil {
				logger.Error("Failed to generate RBAC token", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Server error"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"token": token, "username": creds.Username})
		} else {
			c.JSON(http.StatusOK, gin.H{"message": "Login successful", "username": creds.Username})
		}
	})

	// 健康检查路由
	r.GET("/health", func(c *gin.Context) {
		logger.Info("Health check requested", zap.String("clientIP", c.ClientIP()))
		c.JSON(200, gin.H{"status": "ok"})
	})

	// 受保护路由组
	protected := r.Group("/")
	protected.Use(middleware.Auth())
	routing.Setup(protected, cfg)

	addr := fmt.Sprintf(":%s", cfg.Server.Port)
	logger.Info("Server listening", zap.String("address", addr))
	if err := r.Run(addr); err != nil {
		logger.Error("Failed to start server", zap.Error(err))
	}
}
