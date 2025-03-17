package middleware

import (
	"bytes"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/cache"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

// CacheMiddleware 实现缓存功能的中间件
func CacheMiddleware(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.Caching.Enabled { // 使用 Caching.Enabled
			c.Next()
			return
		}

		path := c.Request.URL.Path
		method := c.Request.Method
		rule := cfg.GetCacheRuleByPath(path)

		if rule == nil || rule.Method != method {
			c.Next()
			return
		}

		if content, found := cache.CheckCache(c.Request.Context(), method, path); found {
			c.String(http.StatusOK, content)
			c.Abort()
			return
		}

		count := cache.IncrementRequestCount(c.Request.Context(), path)
		logger.Debug("Request count", zap.String("path", path), zap.Int64("count", count))

		if count < int64(rule.Threshold) {
			c.Next()
			return
		}

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
