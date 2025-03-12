package traffic

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/observability"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	uberRatelimit "go.uber.org/ratelimit"
	"go.uber.org/zap"
)

// tokenBucketTracer 为令牌桶限流模块初始化追踪器
var tokenBucketTracer = otel.Tracer("ratelimit:token-bucket")

// TokenBucketLimiter 使用 uber-go/ratelimit 实现令牌桶限流器
type TokenBucketLimiter struct {
	limiter uberRatelimit.Limiter // 底层令牌桶限流器
}

// NewTokenBucketLimiter 创建新的令牌桶限流器
// qps: 每秒请求数限制
// burst: 令牌桶的最大突发容量
func NewTokenBucketLimiter(qps, burst int) *TokenBucketLimiter {
	// 使用 uber-go/ratelimit 初始化限流器，设置 slack 为 burst 以支持突发流量
	l := &TokenBucketLimiter{
		limiter: uberRatelimit.New(qps, uberRatelimit.WithSlack(burst)),
	}
	logger.Info("TokenBucketLimiter initialized",
		zap.Int("qps", qps),
		zap.Int("burst", burst))
	return l
}

// Take 获取一个令牌，必要时阻塞以强制执行限流
// 返回令牌被授予的时间
func (tbl *TokenBucketLimiter) Take() time.Time {
	// 阻塞直到有可用令牌，遵循配置的限流速率
	return tbl.limiter.Take()
}

// TokenBucketRateLimit 返回基于令牌桶的限流 Gin 中间件
func TokenBucketRateLimit() gin.HandlerFunc {
	// 获取全局配置
	cfg := config.GetConfig()

	// 使用配置的 QPS 和突发值创建令牌桶限流器
	limiter := NewTokenBucketLimiter(cfg.Traffic.RateLimit.QPS, cfg.Traffic.RateLimit.Burst)

	return func(c *gin.Context) {
		// 如果限流未启用，则跳过
		if !cfg.Traffic.RateLimit.Enabled {
			c.Next()
			return
		}

		// 开始追踪限流决策
		_, span := tokenBucketTracer.Start(c.Request.Context(), "RateLimit.TokenBucket",
			trace.WithAttributes(attribute.String("path", c.Request.URL.Path)))
		defer span.End()

		// 记录当前时间并获取令牌
		now := time.Now()
		takeTime := limiter.Take()
		waitDuration := takeTime.Sub(now)

		// 如果获取令牌需要等待，则因限流拒绝请求
		if waitDuration > 0 {
			logger.Warn("Rate limit exceeded with token bucket",
				zap.String("clientIP", c.ClientIP()),
				zap.String("path", c.Request.URL.Path),
				zap.Duration("waitDuration", waitDuration),
				zap.Int("qps", cfg.Traffic.RateLimit.QPS),
				zap.Int("burst", cfg.Traffic.RateLimit.Burst))
			span.SetStatus(codes.Error, "Rate limit exceeded")
			observability.RateLimitRejections.WithLabelValues(c.Request.URL.Path).Inc()

			// 返回 429 Too Many Requests 响应并包含限流详情
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":      "Request rate limit exceeded",
				"qps":        cfg.Traffic.RateLimit.QPS,
				"burst":      cfg.Traffic.RateLimit.Burst,
				"waitTimeMs": waitDuration.Milliseconds(),
			})
			c.Abort()
			return
		}

		// 令牌立即可用，允许请求继续
		logger.Debug("Request passed token bucket rate limit check",
			zap.String("clientIP", c.ClientIP()),
			zap.String("path", c.Request.URL.Path),
			zap.Time("takeTime", takeTime),
			zap.Int("qps", cfg.Traffic.RateLimit.QPS),
			zap.Int("burst", cfg.Traffic.RateLimit.Burst))
		span.SetStatus(codes.Ok, "Request allowed by token bucket")
		c.Next()
	}
}
