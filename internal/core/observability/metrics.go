package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// 定义全局 Prometheus 指标变量
var (
	// RequestsTotal 记录网关处理的请求总数，按方法、路径和状态码分类
	RequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total number of requests processed by the gateway",
		},
		[]string{"method", "path", "status"},
	)

	// RequestDuration 记录请求延迟分布，按方法和路径分类
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_request_duration_seconds",
			Help:    "Request latency in seconds",
			Buckets: prometheus.DefBuckets, // 默认桶：0.005, 0.01, 0.025, ..., 10
		},
		[]string{"method", "path"},
	)

	// RateLimitRejections 记录因限流拒绝的请求数，按路径分类
	RateLimitRejections = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_rate_limit_rejections_total",
			Help: "Total number of requests rejected due to rate limiting",
		},
		[]string{"path"},
	)

	// BreakerTrips 记录熔断器触发的次数，按路径分类
	BreakerTrips = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_breaker_trips_total",
			Help: "Total number of breaker trips",
		},
		[]string{"path"},
	)

	// ActiveWebSocketConnections 记录当前活跃的 WebSocket 连接数
	ActiveWebSocketConnections = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "gateway_websocket_connections_active",
			Help: "Number of active WebSocket connections",
		},
	)

	// JwtAuthFailures 记录 JWT 鉴权失败的次数，按路径分类
	JwtAuthFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_jwt_auth_failures_total",
			Help: "Total number of JWT authentication failures",
		},
		[]string{"path"},
	)

	// IPAclRejections 记录因 IP 黑白名单拒绝的请求数，按路径和 IP 分类
	IPAclRejections = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_ip_acl_rejections_total",
			Help: "Total number of requests rejected by IP ACL",
		},
		[]string{"path", "ip"},
	)

	// AntiInjectionBlocks 记录因防注入检测拦截的请求数，按路径分类
	AntiInjectionBlocks = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_anti_injection_blocks_total",
			Help: "Total number of requests blocked due to injection detection",
		},
		[]string{"path"},
	)

	// CacheHits 记录缓存命中次数，按路径分类
	CacheHits = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_cache_hits_total",
			Help: "Total number of cache hits",
		},
		[]string{"path"},
	)

	// CacheMisses 记录缓存未命中次数，按路径分类
	CacheMisses = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_cache_misses_total",
			Help: "Total number of cache misses",
		},
		[]string{"path"},
	)

	// GRPCCallsTotal 记录 gRPC 请求总数，按路径分类
	GRPCCallsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_grpc_calls_total",
			Help: "Total number of gRPC calls processed by the gateway",
		},
		[]string{"path", "status"},
	)

	// metricsInitialized 用于确保指标只初始化一次
	metricsInitialized bool
)

// InitMetrics 初始化所有 Prometheus 指标
func InitMetrics() {
	if metricsInitialized {
		return // 避免重复初始化
	}

	// 所有指标已在包级别通过 promauto 自动注册，这里只需标记初始化完成
	metricsInitialized = true
}

// RegisterCustomCounter 注册自定义 Counter 指标
func RegisterCustomCounter(name, help string, labels []string) *prometheus.CounterVec {
	return promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_" + name,
			Help: help,
		},
		labels,
	)
}

// RegisterCustomGauge 注册自定义 Gauge 指标
func RegisterCustomGauge(name, help string) prometheus.Gauge {
	return promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "gateway_" + name,
			Help: help,
		},
	)
}

// RegisterCustomHistogram 注册自定义 Histogram 指标
func RegisterCustomHistogram(name, help string, labels []string, buckets []float64) *prometheus.HistogramVec {
	if buckets == nil {
		buckets = prometheus.DefBuckets // 使用默认桶
	}
	return promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_" + name,
			Help:    help,
			Buckets: buckets,
		},
		labels,
	)
}

// ResetMetrics 重置所有指标（仅用于测试或特殊场景）
func ResetMetrics() {
	RequestsTotal.Reset()
	RequestDuration.Reset()
	RateLimitRejections.Reset()
	BreakerTrips.Reset()
	ActiveWebSocketConnections.Set(0)
	JwtAuthFailures.Reset()
	IPAclRejections.Reset()
	AntiInjectionBlocks.Reset()
	CacheHits.Reset()
	CacheMisses.Reset()
	GRPCCallsTotal.Reset()
}
