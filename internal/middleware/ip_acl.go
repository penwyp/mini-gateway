package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/cache"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	blacklistKey = "mg:ip_blacklist"
	whitelistKey = "mg:ip_whitelist"
)

// IPAcl 中间件实现 IP 黑白名单检查
func IPAcl() gin.HandlerFunc {
	cfg := config.GetConfig()
	return func(c *gin.Context) {
		clientIP := c.ClientIP()
		ctx := context.Background()

		// 检查白名单（优先级最高）
		if len(cfg.Security.IPWhitelist) > 0 {
			isWhitelisted, err := cache.Client.HGet(ctx, whitelistKey, clientIP).Bool()
			if err != nil && err != redis.Nil {
				logger.Error("Failed to check IP whitelist", zap.String("ip", clientIP), zap.Error(err))
			}
			if isWhitelisted {
				logger.Debug("IP allowed by whitelist", zap.String("ip", clientIP))
				c.Next()
				return
			}
			logger.Warn("IP not in whitelist", zap.String("ip", clientIP))
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied by IP whitelist"})
			c.Abort()
			return
		}

		// 检查黑名单
		if len(cfg.Security.IPBlacklist) > 0 {
			isBlacklisted, err := cache.Client.HGet(ctx, blacklistKey, clientIP).Bool()
			if err != nil && err != redis.Nil {
				logger.Error("Failed to check IP blacklist", zap.String("ip", clientIP), zap.Error(err))
			}
			if isBlacklisted {
				logger.Warn("IP blocked by blacklist", zap.String("ip", clientIP))
				c.JSON(http.StatusForbidden, gin.H{"error": "Access denied by IP blacklist"})
				c.Abort()
				return
			}
		}

		c.Next()
	}
}

// InitIPRules 初始化 IP 黑白名单到 Redis
func InitIPRules(cfg *config.Config) {
	ctx := context.Background()

	// 根据 IPUpdateMode 决定是覆盖还是追加
	if cfg.Security.IPUpdateMode == "override" {
		// 覆盖模式：清空现有规则
		cache.Client.Del(ctx, blacklistKey, whitelistKey)
		logger.Info("IP rules cleared due to override mode")
	}

	// 初始化白名单
	if len(cfg.Security.IPWhitelist) > 0 {
		for _, ip := range cfg.Security.IPWhitelist {
			err := cache.Client.HSet(ctx, whitelistKey, ip, "true").Err()
			if err != nil {
				logger.Error("Failed to set IP whitelist in Redis", zap.String("ip", ip), zap.Error(err))
			}
		}
		logger.Info("IP whitelist initialized", zap.Strings("ips", cfg.Security.IPWhitelist))
	}

	// 初始化黑名单
	if len(cfg.Security.IPBlacklist) > 0 {
		for _, ip := range cfg.Security.IPBlacklist {
			err := cache.Client.HSet(ctx, blacklistKey, ip, "true").Err()
			if err != nil {
				logger.Error("Failed to set IP blacklist in Redis", zap.String("ip", ip), zap.Error(err))
			}
		}
		logger.Info("IP blacklist initialized", zap.Strings("ips", cfg.Security.IPBlacklist))
	}
}
