package traffic

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/observability"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// leakyBucketTracer 为漏桶限流模块初始化追踪器
var leakyBucketTracer = otel.Tracer("ratelimit:leaky-bucket")

// LeakyBucketLimiter 实现漏桶限流器
type LeakyBucketLimiter struct {
	capacity int           // 桶容量（突发限制）
	rate     float64       // 漏出速率（每秒请求数，QPS）
	queue    chan struct{} // 表示桶队列的通道
	mutex    sync.Mutex    // 确保队列操作的线程安全
	stopChan chan struct{} // 信号通道，用于停止漏出协程
}

// NewLeakyBucketLimiter 创建新的漏桶限流器
// qps: 每秒请求数限制
// burst: 最大桶容量
func NewLeakyBucketLimiter(qps, burst int) *LeakyBucketLimiter {
	l := &LeakyBucketLimiter{
		capacity: burst,
		rate:     float64(qps),
		queue:    make(chan struct{}, burst),
		stopChan: make(chan struct{}),
	}

	// 启动后台漏出进程
	go l.startLeak()
	logger.Info("LeakyBucketLimiter initialized",
		zap.Int("qps", qps),
		zap.Int("burst", burst))
	return l
}

// startLeak 管理桶的持续漏出，按配置速率进行
func (l *LeakyBucketLimiter) startLeak() {
	// 根据速率计算漏出间隔（例如，1/rate 秒每次漏出）
	ticker := time.NewTicker(time.Second / time.Duration(l.rate))
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.mutex.Lock()
			// 如果队列非空，移除一个请求，模拟漏出
			if len(l.queue) > 0 {
				select {
				case <-l.queue:
				default:
				}
			}
			l.mutex.Unlock()
		case <-l.stopChan:
			logger.Info("LeakyBucketLimiter leak routine stopped")
			return // 收到停止信号时退出
		}
	}
}

// Allow 尝试将请求添加到桶中
// 返回 true 表示成功添加，false 表示桶已满（超出限流）
func (l *LeakyBucketLimiter) Allow() bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	// 非阻塞尝试将请求添加到队列
	select {
	case l.queue <- struct{}{}:
		return true
	default:
		return false // 桶已满
	}
}

// Stop 终止漏出协程并清理资源
func (l *LeakyBucketLimiter) Stop() {
	close(l.stopChan)
}

// LeakyBucketRateLimit 返回基于漏桶的限流 Gin 中间件
func LeakyBucketRateLimit() gin.HandlerFunc {
	// 获取全局配置
	cfg := config.GetConfig()

	// 使用配置的 QPS 和突发值创建漏桶限流器
	limiter := NewLeakyBucketLimiter(cfg.Traffic.RateLimit.QPS, cfg.Traffic.RateLimit.Burst)

	return func(c *gin.Context) {
		// 如果限流未启用，则跳过
		if !cfg.Traffic.RateLimit.Enabled {
			c.Next()
			return
		}

		// 开始追踪限流决策
		_, span := leakyBucketTracer.Start(c.Request.Context(), "RateLimit.LeakyBucket",
			trace.WithAttributes(attribute.String("path", c.Request.URL.Path)))
		defer span.End()

		// 尝试将请求添加到桶中
		if !limiter.Allow() {
			logger.Warn("Rate limit exceeded with leaky bucket",
				zap.String("clientIP", c.ClientIP()),
				zap.String("path", c.Request.URL.Path),
				zap.Int("qps", cfg.Traffic.RateLimit.QPS),
				zap.Int("burst", cfg.Traffic.RateLimit.Burst))
			span.SetStatus(codes.Error, "Rate limit exceeded")
			observability.RateLimitRejections.WithLabelValues(c.Request.URL.Path).Inc()

			// 返回 429 Too Many Requests 响应并包含限流详情
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "Request rate limit exceeded",
				"qps":   cfg.Traffic.RateLimit.QPS,
				"burst": cfg.Traffic.RateLimit.Burst,
			})
			c.Abort()
			return
		}

		// 请求成功添加到桶中，继续处理
		span.SetStatus(codes.Ok, "Request allowed by leaky bucket")
		c.Next()
	}
}

// CleanupLeakyBucket 释放漏桶限流器相关资源
// 应在程序关闭时调用
func CleanupLeakyBucket(limiter *LeakyBucketLimiter) {
	if limiter != nil {
		limiter.Stop()
		logger.Info("LeakyBucketLimiter resources cleaned up")
	}
}
