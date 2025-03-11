package traffic

import (
	"github.com/penwyp/mini-gateway/internal/core/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	uberRatelimit "go.uber.org/ratelimit"
	"go.uber.org/zap"
)

var tokenBucketTracer = otel.Tracer("ratelimit:token-bucket") // 定义限流模块的 Tracer

// TokenBucketLimiter 令牌桶限流器，实现 uber-go/ratelimit 的 Limiter 接口
type TokenBucketLimiter struct {
	limiter uberRatelimit.Limiter
}

// NewTokenBucketLimiter 创建新的令牌桶限流器
// qps: 每秒请求数限制
// burst: 令牌桶突发容量
func NewTokenBucketLimiter(qps, burst int) *TokenBucketLimiter {
	// 创建uber-go/ratelimit的限流器实例
	// WithSlack设置为burst以支持突发流量
	return &TokenBucketLimiter{
		limiter: uberRatelimit.New(qps, uberRatelimit.WithSlack(burst)),
	}
}

// Take 获取令牌，会阻塞直到满足速率限制
// 返回获取令牌的时间
func (tbl *TokenBucketLimiter) Take() time.Time {
	// 调用底层uber-go/ratelimit的Take方法，会阻塞以确保速率限制
	return tbl.limiter.Take()
}

// TokenBucketRateLimit 令牌桶限流中间件
func TokenBucketRateLimit() gin.HandlerFunc {
	// 获取全局配置
	cfg := config.GetConfig()

	// 创建令牌桶限流器，使用配置中的QPS和burst参数
	limiter := NewTokenBucketLimiter(cfg.Traffic.RateLimit.QPS, cfg.Traffic.RateLimit.Burst)

	return func(c *gin.Context) {
		// 检查是否启用限流
		if !cfg.Traffic.RateLimit.Enabled {
			c.Next()
			return
		}

		_, span := tokenBucketTracer.Start(c.Request.Context(), "RateLimit.TokenBucket",
			trace.WithAttributes(attribute.String("path", c.Request.URL.Path)))
		defer span.End()

		// 获取当前时间
		now := time.Now()
		// 调用Take方法，可能阻塞以满足速率限制
		takeTime := limiter.Take()

		// 计算等待时间
		waitDuration := takeTime.Sub(now)

		// 如果需要等待（waitDuration > 0），说明令牌不足，拒绝请求
		if waitDuration > 0 {
			logger.Warn("令牌桶限流触发",
				zap.String("clientIP", c.ClientIP()),
				zap.String("path", c.Request.URL.Path),
				zap.Duration("waitDuration", waitDuration),
				zap.Int("qps", cfg.Traffic.RateLimit.QPS),
				zap.Int("burst", cfg.Traffic.RateLimit.Burst),
			)

			span.SetStatus(codes.Error, "Rate limit exceeded")
			observability.RateLimitRejections.WithLabelValues(c.Request.URL.Path).Inc()

			// 返回429 Too Many Requests
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":      "请求频率超过限制",
				"qps":        cfg.Traffic.RateLimit.QPS,
				"burst":      cfg.Traffic.RateLimit.Burst,
				"waitTimeMs": waitDuration.Milliseconds(),
			})
			c.Abort()
			return
		}

		// 令牌立即可用，继续处理请求
		logger.Debug("令牌桶限流检查通过",
			zap.String("clientIP", c.ClientIP()),
			zap.String("path", c.Request.URL.Path),
			zap.Time("takeTime", takeTime),
			zap.Int("qps", cfg.Traffic.RateLimit.QPS),
			zap.Int("burst", cfg.Traffic.RateLimit.Burst),
		)
		span.SetStatus(codes.Ok, "Request allowed")
		c.Next()
	}
}
