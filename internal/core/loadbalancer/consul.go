package loadbalancer

import (
	"encoding/json"
	"fmt"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

var cTracer = otel.Tracer("loadbalancer:consu") // 定义负载均衡模块的 Tracer

type ConsulBalancer struct {
	client *api.Client
	rules  map[string][]string
	mu     sync.RWMutex
	stopCh chan struct{}
}

func NewConsulBalancer(consulAddr string) (*ConsulBalancer, error) {
	config := api.DefaultConfig()
	config.Address = consulAddr
	client, err := api.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Consul client: %v", err)
	}

	cb := &ConsulBalancer{
		client: client,
		rules:  make(map[string][]string),
		stopCh: make(chan struct{}),
	}
	go cb.watchRules()
	return cb, nil
}

func (cb *ConsulBalancer) SelectTarget(targets []string, req *http.Request) string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	// 开始追踪负载均衡选择
	_, span := cTracer.Start(req.Context(), "LoadBalancer.Select",
		trace.WithAttributes(attribute.Int("target_count", len(targets))))
	defer span.End()

	path := req.URL.Path
	if targets, ok := cb.rules[path]; ok && len(targets) > 0 {
		count := uint32(len(targets))
		index := uint32(time.Now().UnixNano()) % count
		target := targets[index]
		span.SetAttributes(attribute.String("selected_target", target))
		logger.Debug("负载均衡选择的目标", zap.String("target", target))
		return target
	}

	if len(targets) == 0 {
		return ""
	}
	count := uint32(len(targets))
	index := uint32(time.Now().UnixNano()) % count
	target := targets[index]
	span.SetAttributes(attribute.String("selected_target", target))
	logger.Debug("负载均衡选择的目标", zap.String("target", target))
	return target

}

func (cb *ConsulBalancer) watchRules() {
	var lastIndex uint64
	for {
		select {
		case <-cb.stopCh:
			return
		default:
			kv, meta, err := cb.client.KV().Get("gateway/loadbalancer/rules", &api.QueryOptions{
				WaitIndex: lastIndex,
			})
			if err != nil || kv == nil {
				logger.Error("Failed to fetch load balancer rules from Consul",
					zap.Error(err),
				)
				time.Sleep(5 * time.Second)
				continue
			}

			lastIndex = meta.LastIndex
			var newRules map[string][]string
			if err := json.Unmarshal(kv.Value, &newRules); err != nil {
				logger.Error("Failed to unmarshal load balancer rules",
					zap.Error(err),
				)
				time.Sleep(5 * time.Second)
				continue
			}

			cb.mu.Lock()
			cb.rules = newRules
			cb.mu.Unlock()

			logger.Info("Updated load balancer rules from Consul",
				zap.Any("rules", newRules),
			)

			time.Sleep(1 * time.Second)
		}
	}
}

func (cb *ConsulBalancer) Stop() {
	close(cb.stopCh)
}
