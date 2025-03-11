package loadbalancer

import (
	"net/http"
	"sync"

	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

var rrTracer = otel.Tracer("loadbalancer:round-robin") // 定义负载均衡模块的 Tracer

type RoundRobin struct {
	next uint32
	mu   sync.Mutex
}

func NewRoundRobin() LoadBalancer {
	return &RoundRobin{}
}

func (rr *RoundRobin) SelectTarget(targets []string, r *http.Request) string {
	// 开始追踪负载均衡选择
	_, span := rrTracer.Start(r.Context(), "LoadBalancer.Select",
		trace.WithAttributes(attribute.Int("target_count", len(targets))))
	defer span.End()

	if len(targets) == 0 {
		logger.Warn("没有可用的目标")
		span.SetAttributes(attribute.String("result", "no targets"))
		return ""
	}

	rr.mu.Lock()
	defer rr.mu.Unlock()
	target := targets[rr.next%uint32(len(targets))]
	rr.next++

	// 记录选择的目标
	span.SetAttributes(attribute.String("selected_target", target))
	logger.Debug("负载均衡选择的目标", zap.String("target", target))
	return target
}

func (rr *RoundRobin) UpdateTargets(cfg *config.Config) {
	// 此方法无需 tracing，因为它是配置更新，不涉及请求处理
}
