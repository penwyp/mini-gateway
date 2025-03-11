package routing

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/loadbalancer"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

var regexpTracer = otel.Tracer("router:regexp") // 定义路由模块的 Tracer

type RegexpRouter struct {
	rules map[string]*regexp.Regexp
	lb    loadbalancer.LoadBalancer
}

func NewRegexpRouter(cfg *config.Config) *RegexpRouter {
	lb, err := loadbalancer.NewLoadBalancer(cfg.Routing.LoadBalancer, cfg)
	if err != nil {
		logger.Error("创建负载均衡器失败", zap.Error(err))
		lb = loadbalancer.NewRoundRobin()
	}
	return &RegexpRouter{
		rules: make(map[string]*regexp.Regexp),
		lb:    lb,
	}
}

func (rr *RegexpRouter) Setup(r gin.IRouter, cfg *config.Config) {
	rules := cfg.Routing.GetHTTPRules()
	if len(rules) == 0 {
		logger.Warn("配置中未找到路由规则")
		return
	}

	for path, targetRules := range rules {
		pattern := "^" + path + "$"
		re, err := regexp.Compile(pattern)
		if err != nil {
			logger.Error("编译正则表达式模式失败",
				zap.String("path", path),
				zap.Error(err))
			continue
		}
		rr.rules[path] = re
		logger.Info("在 Regexp 中注册路由",
			zap.String("path", path),
			zap.Any("targets", targetRules))
	}

	r.Use(func(c *gin.Context) {
		// 开始追踪路由匹配
		ctx, span := regexpTracer.Start(c.Request.Context(), "Routing.Match",
			trace.WithAttributes(attribute.String("path", c.Request.URL.Path)))
		defer span.End()

		path := c.Request.URL.Path
		var targetRules config.RoutingRules
		var found bool

		for pattern, re := range rr.rules {
			if re.MatchString(path) {
				targetRules = cfg.Routing.Rules[pattern]
				found = true
				break
			}
		}

		if !found {
			logger.Warn("路由未找到",
				zap.String("path", path),
				zap.String("method", c.Request.Method))
			c.JSON(http.StatusNotFound, gin.H{"error": "路由未找到"})
			c.Abort()
			return
		}

		// 记录匹配成功的目标
		span.SetAttributes(attribute.String("matched_target", targetRules[0].Target))
		span.SetStatus(codes.Ok, "Route matched")
		logger.Info("路由匹配成功", zap.String("path", path), zap.Any("rules", targetRules))

		// 将追踪上下文传递给下游
		c.Request = c.Request.WithContext(ctx)
		createProxyHandler(targetRules, rr.lb)(c)
	})
}
