package traffic

import (
	"github.com/penwyp/mini-gateway/internal/core/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"net/http"
	"sync"
	"time"

	"github.com/afex/hystrix-go/hystrix"
	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

var breakerTimeSlidingTracer = otel.Tracer("breaker:time-sliding")

// RequestStat 记录单次请求的状态
type RequestStat struct {
	Success   bool
	Latency   time.Duration
	Timestamp time.Time
}

// TimeSlidingWindow 基于时间的滑动窗口
type TimeSlidingWindow struct {
	requests []RequestStat
	mutex    sync.RWMutex
	duration time.Duration
}

// NewTimeSlidingWindow 创建时间滑动窗口
func NewTimeSlidingWindow(duration time.Duration) *TimeSlidingWindow {
	sw := &TimeSlidingWindow{
		requests: make([]RequestStat, 0),
		duration: duration,
	}
	go sw.cleanup() // 启动定时清理
	return sw
}

// Update 更新时间滑动窗口
func (sw *TimeSlidingWindow) Update(stat RequestStat) {
	sw.mutex.Lock()
	defer sw.mutex.Unlock()
	sw.requests = append(sw.requests, stat)
}

// cleanup 定时清理过期数据
func (sw *TimeSlidingWindow) cleanup() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		sw.mutex.Lock()
		now := time.Now()
		var validRequests []RequestStat
		for _, stat := range sw.requests {
			if now.Sub(stat.Timestamp) <= sw.duration {
				validRequests = append(validRequests, stat)
			}
		}
		sw.requests = validRequests
		sw.mutex.Unlock()
	}
}

// ErrorRate 计算错误率
func (sw *TimeSlidingWindow) ErrorRate() float64 {
	sw.mutex.RLock()
	defer sw.mutex.RUnlock()
	if len(sw.requests) == 0 {
		return 0
	}
	var total, failed int
	for _, stat := range sw.requests {
		total++
		if !stat.Success {
			failed++
		}
	}
	return float64(failed) / float64(total)
}

// AvgLatency 计算平均响应时间
func (sw *TimeSlidingWindow) AvgLatency() time.Duration {
	sw.mutex.RLock()
	defer sw.mutex.RUnlock()
	if len(sw.requests) == 0 {
		return 0
	}
	var totalLatency time.Duration
	for _, stat := range sw.requests {
		totalLatency += stat.Latency
	}
	return totalLatency / time.Duration(len(sw.requests))
}

// Prometheus 指标
var (
	errorRateGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_error_rate",
			Help: "Error rate of requests per route",
		},
		[]string{"path"},
	)
	latencyGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_avg_latency_seconds",
			Help: "Average latency of requests per route in seconds",
		},
		[]string{"path"},
	)
)

func init() {
	prometheus.MustRegister(errorRateGauge, latencyGauge)
}

// Breaker 中间件实现熔断降级
func Breaker() gin.HandlerFunc {
	cfg := config.GetConfig()
	if !cfg.Middleware.Breaker || !cfg.Traffic.Breaker.Enabled {
		return func(c *gin.Context) {
			c.Next() // 熔断器未启用，直接放行
		}
	}

	// 为每个路由配置 Hystrix
	for path := range cfg.Routing.Rules {
		hystrix.ConfigureCommand(path, hystrix.CommandConfig{
			Timeout:                cfg.Traffic.Breaker.Timeout,
			MaxConcurrentRequests:  cfg.Traffic.Breaker.MaxConcurrent,
			RequestVolumeThreshold: cfg.Traffic.Breaker.MinRequests,
			SleepWindow:            cfg.Traffic.Breaker.SleepWindow,
			ErrorPercentThreshold:  int(cfg.Traffic.Breaker.ErrorRate * 100),
		})
	}

	// 初始化时间滑动窗口
	window := NewTimeSlidingWindow(time.Duration(cfg.Traffic.Breaker.WindowDuration) * time.Second)

	return func(c *gin.Context) {
		_, span := breakerTimeSlidingTracer.Start(c.Request.Context(), "Breaker.Check",
			trace.WithAttributes(attribute.String("path", c.Request.URL.Path)))
		defer span.End()

		start := time.Now()
		path := c.Request.URL.Path

		err := hystrix.Do(path, func() error {
			c.Next() // 执行下游请求
			return c.Err()
		}, func(err error) error {
			// 降级逻辑
			logger.Warn("Service circuit breaker triggered",
				zap.String("path", path),
				zap.Error(err),
			)
			span.SetStatus(codes.Error, "Circuit breaker open")
			span.SetAttributes(attribute.String("breaker_state", "open"))
			observability.BreakerTrips.WithLabelValues(path).Inc()
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Service unavailable"})
			c.Abort()
			return nil
		})

		latency := time.Since(start)
		success := err == nil && c.Writer.Status() < 400
		window.Update(RequestStat{
			Success:   success,
			Latency:   latency,
			Timestamp: time.Now(),
		})

		// 更新 Prometheus 指标
		errorRate := window.ErrorRate()
		avgLatency := window.AvgLatency()
		errorRateGauge.WithLabelValues(path).Set(errorRate)
		latencyGauge.WithLabelValues(path).Set(float64(avgLatency) / float64(time.Second))
		span.SetStatus(codes.Ok, "Request processed")

		// 日志记录统计信息
		logger.Debug("Request stats",
			zap.String("path", path),
			zap.Bool("success", success),
			zap.Duration("latency", latency),
			zap.Float64("errorRate", errorRate),
			zap.Duration("avgLatency", avgLatency),
		)
	}
}
