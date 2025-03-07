package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

// RateLimit 限流中间件（基于令牌桶算法）
func RateLimit() gin.HandlerFunc {
	cfg := config.GetConfig()
	if !cfg.Traffic.RateLimit.Enabled {
		return func(c *gin.Context) {
			c.Next() // 如果限流未启用，直接放行
		}
	}

	// 创建令牌桶限流器
	limiter := rate.NewLimiter(rate.Limit(cfg.Traffic.RateLimit.QPS), cfg.Traffic.RateLimit.Burst)
	logger.Info("Rate limiter initialized",
		zap.Int("qps", cfg.Traffic.RateLimit.QPS),
		zap.Int("burst", cfg.Traffic.RateLimit.Burst),
	)

	return func(c *gin.Context) {
		// 检查是否有足够的令牌
		if !limiter.Allow() {
			logger.Warn("Rate limit exceeded",
				zap.String("path", c.Request.URL.Path),
				zap.String("clientIP", c.ClientIP()),
			)
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many requests"})
			c.Abort()
			return
		}

		// 继续处理请求
		c.Next()
	}
}
