package health

import (
	"context"
	"sort"
	"sync"
	"time"

	"net/url"

	"github.com/gorilla/websocket"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// TargetStatus 后端目标的状态信息
type TargetStatus struct {
	Rule              string    `json:"rule"`                // 规则名称
	URL               string    `json:"url"`                 // 目标地址
	Protocol          string    `json:"protocol"`            // 协议类型
	RequestCount      int64     `json:"request_count"`       // 业务请求总数
	SuccessCount      int64     `json:"success_count"`       // 业务成功次数
	CacheHitCount     int64     `json:"cache_hit_count"`     // 缓存命中次数
	FailureCount      int64     `json:"failure_count"`       // 业务失败次数
	ProbeRequestCount int64     `json:"probe_request_count"` // 探活请求总数
	ProbeSuccessCount int64     `json:"probe_success_count"` // 探活成功次数
	ProbeFailureCount int64     `json:"probe_failure_count"` // 探活失败次数
	LastProbeTime     time.Time `json:"last_probe_time"`     // 最后一次探活时间
	LastRequestTime   time.Time `json:"last_request_time"`   // 最后一次业务请求时间
}

// HealthChecker 健康检查服务
type HealthChecker struct {
	targetStats map[string]*TargetStatus // 目标地址到状态信息的映射
	healthPaths map[string]string        // 目标地址到健康检查路径的映射
	mu          sync.RWMutex             // 读写锁
	cfg         *config.Config           // 配置
	cleanupCh   chan struct{}            // 清理信号通道
}

var (
	globalHealthChecker *HealthChecker
	once                sync.Once
)

// GetGlobalHealthChecker 获取全局健康检查实例
func GetGlobalHealthChecker() *HealthChecker {
	return globalHealthChecker
}

// InitHealthChecker 创建并初始化健康检查服务
func InitHealthChecker(cfg *config.Config) *HealthChecker {
	logger.Info("Initializing health checker service")
	checker := &HealthChecker{
		targetStats: make(map[string]*TargetStatus),
		healthPaths: make(map[string]string),
		cfg:         cfg,
		cleanupCh:   make(chan struct{}),
	}
	checker.RefreshTargets(cfg) // 初始化时刷新目标
	go checker.startHeartbeat()

	once.Do(func() {
		globalHealthChecker = checker
	})
	return checker
}

// RefreshTargets 刷新目标状态和健康检查路径
func (h *HealthChecker) RefreshTargets(cfg *config.Config) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// 更新配置
	h.cfg = cfg

	// 保存旧的状态，以便迁移统计数据（可选）
	oldStats := h.targetStats

	// 重置目标和健康检查路径
	h.targetStats = make(map[string]*TargetStatus)
	h.healthPaths = make(map[string]string)

	for ruleName, rules := range cfg.Routing.Rules {
		for _, rule := range rules {
			host, err := normalizeTarget(rule)
			if err != nil {
				logger.Error("Invalid target address",
					zap.String("target", rule.Target),
					zap.Error(err))
				continue
			}

			// 设置健康检查路径
			if rule.HealthCheckPath != "" {
				h.healthPaths[host] = rule.HealthCheckPath
			} else {
				h.healthPaths[host] = "/health"
			}

			// 初始化或迁移目标状态
			if oldStat, ok := oldStats[host]; ok {
				// 迁移旧统计数据
				h.targetStats[host] = oldStat
			} else {
				h.targetStats[host] = &TargetStatus{
					Rule:              ruleName,
					URL:               rule.Target,
					Protocol:          rule.Protocol,
					RequestCount:      0,
					SuccessCount:      0,
					FailureCount:      0,
					ProbeRequestCount: 0,
					ProbeSuccessCount: 0,
					ProbeFailureCount: 0,
					LastProbeTime:     time.Time{},
				}
				logger.Info("Initialized new health check target",
					zap.String("target", host),
					zap.String("protocol", rule.Protocol),
					zap.String("healthCheckPath", h.healthPaths[host]))
			}
		}
	}
	logger.Info("Health checker targets refreshed",
		zap.Int("totalTargets", len(h.targetStats)))
}

// normalizeTarget 规范化目标地址
func normalizeTarget(target config.RoutingRule) (string, error) {
	if target.Protocol == "grpc" {
		return target.Target, nil
	}
	u, err := url.Parse(target.Target)
	if err != nil {
		return "", err
	}
	return u.Host, nil
}

// normalizeTargetHost 规范化目标主机地址
func normalizeTargetHost(target string) (string, error) {
	u, err := url.Parse(target)
	if err != nil {
		return target, err
	}
	return u.Host, nil
}

// startHeartbeat 开始周期性心跳检测
func (h *HealthChecker) startHeartbeat() {
	ticker := time.NewTicker(1 * time.Second) // 初始 ticker，避免第一次等待
	defer ticker.Stop()

	for {
		select {
		case <-h.cleanupCh:
			logger.Info("Stopping heartbeat checks")
			return
		case <-ticker.C:
			h.mu.RLock()
			heartbeatInterval := 30 * time.Second
			if h.cfg.Routing.HeartbeatInterval > 0 {
				heartbeatInterval = time.Duration(h.cfg.Routing.HeartbeatInterval) * time.Second
			}
			h.mu.RUnlock()

			h.performHeartbeatCheck()
			ticker.Reset(heartbeatInterval) // 重置 ticker 以适应可能的配置变更
		}
	}
}

// performHeartbeatCheck 执行一次心跳检测
func (h *HealthChecker) performHeartbeatCheck() {
	h.mu.RLock()
	defer h.mu.RUnlock()

	logger.Info("Starting heartbeat check",
		zap.Int("targetCount", len(h.targetStats)),
		zap.String("timestamp", time.Now().Format("2006-01-02 15:04:05")))

	for target, stat := range h.targetStats {
		healthPath, ok := h.healthPaths[target]
		if !ok {
			healthPath = "/health"
		}

		now := time.Now()
		stat.LastProbeTime = now
		stat.ProbeRequestCount++

		switch stat.Protocol {
		case "http", "":
			h.checkHTTP(target, healthPath, stat)
		case "grpc":
			h.checkGRPC(target, stat)
		case "websocket":
			h.checkWebSocket(stat.URL, healthPath, stat)
		default:
			logger.Warn("Unsupported protocol, skipping health check",
				zap.String("protocol", stat.Protocol),
				zap.String("target", target))
		}
	}
}

// checkHTTP 检查 HTTP 目标健康状态
func (h *HealthChecker) checkHTTP(target, healthPath string, stat *TargetStatus) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI("http://" + target + healthPath)
	req.Header.SetMethod("HEAD")

	client := &fasthttp.Client{
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	err := client.DoTimeout(req, resp, 5*time.Second)
	if err != nil || resp.StatusCode() >= 400 {
		stat.ProbeFailureCount++
		logger.Warn("HTTP heartbeat check failed",
			zap.String("target", target),
			zap.String("healthPath", healthPath),
			zap.Error(err),
			zap.Int("statusCode", resp.StatusCode()))
		return
	}
	stat.ProbeSuccessCount++
	logger.Info("HTTP heartbeat check succeeded",
		zap.String("target", target),
		zap.String("healthPath", healthPath))
}

// checkGRPC 检查 gRPC 目标健康状态
func (h *HealthChecker) checkGRPC(target string, stat *TargetStatus) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, target, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		stat.ProbeFailureCount++
		logger.Warn("gRPC dial failed",
			zap.String("target", target),
			zap.Error(err))
		return
	}
	defer conn.Close()

	healthPath, ok := h.healthPaths[target]
	if !ok {
		healthPath = "" // 默认检查整个服务器
	}

	client := grpc_health_v1.NewHealthClient(conn)
	serviceName := healthPath
	if serviceName == "/health" {
		serviceName = "" // 检查整个服务器
	}

	resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: serviceName})
	if err != nil || (resp != nil && resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING) {
		stat.ProbeFailureCount++
		var statusStr string
		if resp != nil {
			statusStr = resp.GetStatus().String()
		} else {
			statusStr = "UNKNOWN"
		}
		logger.Warn("gRPC health check failed",
			zap.String("target", target),
			zap.String("service", serviceName),
			zap.Error(err),
			zap.String("status", statusStr))
		return
	}

	stat.ProbeSuccessCount++
	logger.Info("gRPC health check succeeded",
		zap.String("target", target),
		zap.String("service", serviceName))
}

// checkWebSocket 检查 WebSocket 目标健康状态
func (h *HealthChecker) checkWebSocket(target, healthPath string, stat *TargetStatus) {
	dialer := websocket.DefaultDialer
	fullURL := target + healthPath
	conn, _, err := dialer.Dial(fullURL, nil)
	if err != nil {
		stat.ProbeFailureCount++
		logger.Warn("WebSocket heartbeat check failed",
			zap.String("target", target),
			zap.String("healthPath", healthPath),
			zap.String("fullURL", fullURL),
			zap.Error(err))
		return
	}
	defer conn.Close()
	stat.ProbeSuccessCount++
	logger.Info("WebSocket heartbeat check succeeded",
		zap.String("target", target),
		zap.String("healthPath", healthPath),
		zap.String("fullURL", fullURL))
}

// UpdateRequestCount 更新业务请求计数
func (h *HealthChecker) UpdateRequestCount(target string, success bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	host, _ := normalizeTargetHost(target)
	if stat, ok := h.targetStats[host]; ok {
		stat.RequestCount++
		if success {
			stat.SuccessCount++
		} else {
			stat.FailureCount++
		}
		stat.LastRequestTime = time.Now()
	} else {
		logger.Warn("Target not found, unable to update request count",
			zap.String("target", target))
	}
}

// ResetAllStats 重置所有后端目标的状态信息
func (h *HealthChecker) ResetAllStats() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, stat := range h.targetStats {
		stat.RequestCount = 0
		stat.SuccessCount = 0
		stat.FailureCount = 0
		stat.ProbeRequestCount = 0
		stat.ProbeSuccessCount = 0
		stat.ProbeFailureCount = 0
		stat.LastProbeTime = time.Time{}
	}
	logger.Info("All target stats reset")
}

// GetAllStats 获取所有后端目标的状态信息
func (h *HealthChecker) GetAllStats() []TargetStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()

	stats := make([]TargetStatus, 0, len(h.targetStats))
	for _, stat := range h.targetStats {
		stats = append(stats, *stat)
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Protocol == stats[j].Protocol {
			return stats[i].URL < stats[j].URL
		}
		if stats[i].Rule == stats[j].Rule {
			return stats[i].Protocol < stats[j].Protocol
		}
		return stats[i].Rule < stats[j].Rule
	})
	return stats
}

// Close 关闭健康检查服务
func (h *HealthChecker) Close() {
	close(h.cleanupCh)
	logger.Info("Health checker service closed")
}
