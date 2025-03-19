package middleware

import (
	"bytes"
	"context"
	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/cache"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
	"net/http"
)

func CacheMiddleware() gin.HandlerFunc {
	if config.GetConfig().Caching.Enabled {
		ctx := context.Background()
		for urlKey := range config.GetConfig().Routing.Rules {
			cache.ClearMethodCount(ctx, "GET", urlKey)
			cache.ClearMethodCount(ctx, "POST", urlKey)
			cache.ClearRequestCount(ctx, urlKey)
		}
	}

	return func(c *gin.Context) {
		if !config.GetConfig().Caching.Enabled {
			c.Next()
			return
		}

		path := c.Request.URL.Path
		method := c.Request.Method
		rule := config.GetConfig().GetCacheRuleByPath(path)

		if rule == nil || rule.Method != method {
			c.Next()
			return
		}

		// 检查是否已存在缓存
		if content, found := cache.CheckCache(c.Request.Context(), method, path); found {
			// 缓存命中，更新请求计数和缓存命中计数
			cache.IncrementRequestCount(c.Request.Context(), path, rule.TTL)
			c.String(http.StatusOK, content)
			c.Abort()
			return
		}

		// 使用传入TTL更新请求计数，统计在当前窗口内的请求数
		count := cache.IncrementRequestCount(c.Request.Context(), path, rule.TTL)
		logger.Debug("Request count", zap.String("path", path), zap.Int64("count", count))

		// 如果当前窗口内请求次数未达到阈值，不进行缓存
		if count < int64(rule.Threshold) {
			c.Next()
			return
		}

		// 当请求次数达到阈值后，拦截响应进行缓存
		writer := &responseWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		c.Next()

		if c.Writer.Status() == http.StatusOK {
			content := writer.body.String()
			err := cache.SetCache(c.Request.Context(), method, path, content, rule.TTL)
			if err != nil {
				logger.Error("Failed to cache response", zap.Error(err))
			}
		}
	}
}

// responseWriter 用于捕获响应内容
type responseWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.body == nil {
		w.body = bytes.NewBuffer(nil)
	}
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}
