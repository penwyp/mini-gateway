package main

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/routing"
	"github.com/penwyp/mini-gateway/internal/middleware"
	"github.com/penwyp/mini-gateway/pkg/logger"
)

func main() {
	// 1. 初始化配置
	cfg := config.InitConfig()

	// 2. 初始化日志
	logger.Init(logger.Config{
		Level:      cfg.Logger.Level,
		FilePath:   cfg.Logger.FilePath,
		MaxSize:    cfg.Logger.MaxSize,
		MaxBackups: cfg.Logger.MaxBackups,
		MaxAge:     cfg.Logger.MaxAge,
		Compress:   cfg.Logger.Compress,
	})

	// 记录启动信息
	logger.Info("Gateway starting",
		zap.String("port", cfg.Server.Port),
		zap.String("configPath", "config/config.yaml"),
	)

	// 3. 初始化 Gin 引擎
	r := gin.Default()

	// 4. 注册中间件（示例）
	r.Use(middleware.Auth(), middleware.RateLimit())

	// 5. 初始化路由模块
	routing.Setup(r, cfg)

	// 6. 添加健康检查端点（示例）
	r.GET("/health", func(c *gin.Context) {
		logger.Info("Health check requested", zap.String("clientIP", c.ClientIP()))
		c.JSON(200, gin.H{"status": "ok"})
	})

	// 7. 启动服务
	addr := fmt.Sprintf(":%s", cfg.Server.Port)
	logger.Info("Server listening", zap.String("address", addr))
	if err := r.Run(addr); err != nil {
		logger.Error("Failed to start server", zap.Error(err))
	}
}
