package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

// LeakyBucketLimiter 漏桶限流器
type LeakyBucketLimiter struct {
	capacity int           // 桶的容量（burst）
	rate     float64       // 每秒漏出速率（QPS）
	queue    chan struct{} // 代表桶的队列
	mutex    sync.Mutex    // 确保线程安全
	stopChan chan struct{} // 用于停止漏水goroutine
}

// NewLeakyBucketLimiter 创建新的漏桶限流器
// qps: 每秒请求数限制
// burst: 桶的容量
func NewLeakyBucketLimiter(qps, burst int) *LeakyBucketLimiter {
	l := &LeakyBucketLimiter{
		capacity: burst,
		rate:     float64(qps),
		queue:    make(chan struct{}, burst),
		stopChan: make(chan struct{}),
	}

	// 启动漏水goroutine
	go l.startLeak()

	return l
}

// startLeak 处理桶的持续漏水
func (l *LeakyBucketLimiter) startLeak() {
	// 根据速率计算漏水间隔
	ticker := time.NewTicker(time.Second / time.Duration(l.rate))
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.mutex.Lock()
			// 如果队列中有请求，移除一个（模拟漏水）
			if len(l.queue) > 0 {
				select {
				case <-l.queue:
				default:
				}
			}
			l.mutex.Unlock()
		case <-l.stopChan:
			// 收到停止信号，退出goroutine
			return
		}
	}
}

// Allow 尝试将请求加入桶中
// 返回true表示成功加入，false表示桶满被限流
func (l *LeakyBucketLimiter) Allow() bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	// 非阻塞尝试加入队列
	select {
	case l.queue <- struct{}{}:
		return true
	default:
		return false
	}
}

// Stop 清理限流器资源
func (l *LeakyBucketLimiter) Stop() {
	close(l.stopChan)
}

// LeakyBucketRateLimit 漏桶限流中间件
func LeakyBucketRateLimit() gin.HandlerFunc {
	// 获取全局配置
	cfg := config.GetConfig()

	// 创建漏桶限流器，使用配置中的QPS和burst参数
	limiter := NewLeakyBucketLimiter(cfg.Traffic.RateLimit.QPS, cfg.Traffic.RateLimit.Burst)

	return func(c *gin.Context) {
		// 检查是否启用限流
		if !cfg.Traffic.RateLimit.Enabled {
			c.Next()
			return
		}

		// 尝试将请求加入桶中
		if !limiter.Allow() {
			// 记录限流日志
			logger.Warn("漏桶限流触发",
				zap.String("clientIP", c.ClientIP()),
				zap.String("path", c.Request.URL.Path),
				zap.Int("qps", cfg.Traffic.RateLimit.QPS),
				zap.Int("burst", cfg.Traffic.RateLimit.Burst),
			)

			// 返回429 Too Many Requests
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "请求频率超过限制",
				"qps":   cfg.Traffic.RateLimit.QPS,
				"burst": cfg.Traffic.RateLimit.Burst,
			})
			c.Abort()
			return
		}

		// 成功加入桶，继续处理请求
		c.Next()
	}
}

// CleanupLeakyBucket 清理漏桶限流器资源
// 应该在程序关闭时调用
func CleanupLeakyBucket(limiter *LeakyBucketLimiter) {
	if limiter != nil {
		limiter.Stop()
	}
}
