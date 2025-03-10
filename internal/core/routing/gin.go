package routing

import (
	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/loadbalancer"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

type GinRouter struct {
	lb loadbalancer.LoadBalancer
}

func NewGinRouter(cfg *config.Config) *GinRouter {
	lb, err := loadbalancer.NewLoadBalancer(cfg.Routing.LoadBalancer, cfg)
	if err != nil {
		logger.Error("创建负载均衡器失败", zap.Error(err))
		lb = loadbalancer.NewRoundRobin()
	}
	return &GinRouter{lb: lb}
}

func (gr *GinRouter) Setup(r gin.IRouter, cfg *config.Config) {
	rules := cfg.Routing.Rules
	if len(rules) == 0 {
		logger.Warn("配置中未找到路由规则")
		return
	}

	for path, targetRules := range rules {
		logger.Info("在 Gin 中注册路由",
			zap.String("path", path),
			zap.Any("targets", targetRules))
		r.Any(path, createProxyHandler(targetRules, gr.lb))
	}
}
