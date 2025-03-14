package routing

import (
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/loadbalancer"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// regexpTracer 为正则路由模块初始化追踪器
var regexpTracer = otel.Tracer("router:regexp")

// RegexpRouter 使用正则表达式和负载均衡处理路由逻辑
type RegexpRouter struct {
	rules map[string]*regexp.Regexp // 路径到正则表达式的映射
	lb    loadbalancer.LoadBalancer // 负载均衡器实例
}

// NewRegexpRouter 根据配置创建并初始化 RegexpRouter 实例
func NewRegexpRouter(cfg *config.Config) *RegexpRouter {
	lb, err := loadbalancer.NewLoadBalancer(cfg.Routing.LoadBalancer, cfg)
	if err != nil {
		logger.Error("Failed to initialize load balancer",
			zap.String("type", cfg.Routing.LoadBalancer),
			zap.Error(err))
		lb = loadbalancer.NewRoundRobin() // 初始化失败时回退到轮询
	}
	return &RegexpRouter{
		rules: make(map[string]*regexp.Regexp),
		lb:    lb,
	}
}

// Setup 根据配置在 Gin 路由器中设置 HTTP 路由规则
func (rr *RegexpRouter) Setup(r gin.IRouter, httpProxy *HTTPProxy, cfg *config.Config) {
	rules := cfg.Routing.GetHTTPRules()
	if len(rules) == 0 {
		logger.Warn("No HTTP routing rules found in configuration")
		return
	}

	// 编译并注册路由规则
	for path, targetRules := range rules {
		pattern := "^" + path + "$" // 为精确匹配添加锚点
		re, err := regexp.Compile(pattern)
		if err != nil {
			logger.Error("Failed to compile regular expression for route",
				zap.String("path", path),
				zap.Error(err))
			continue
		}
		rr.rules[path] = re
		logger.Info("Successfully registered route in RegexpRouter",
			zap.String("path", path),
			zap.Any("targets", targetRules))
	}

	// 中间件：处理路由匹配和代理转发
	r.Use(func(c *gin.Context) {
		// 开始追踪路由匹配过程
		ctx, span := regexpTracer.Start(c.Request.Context(), "Routing.Match",
			trace.WithAttributes(attribute.String("path", c.Request.URL.Path)))
		defer span.End()

		path := c.Request.URL.Path
		var targetRules config.RoutingRules
		var found bool

		// 匹配请求路径与已注册的正则模式
		for pattern, re := range rr.rules {
			if re.MatchString(path) {
				targetRules = cfg.Routing.Rules[pattern]
				found = true
				break
			}
		}

		// 未找到匹配路由时的处理
		if !found {
			logger.Warn("No matching route found",
				zap.String("path", path),
				zap.String("method", c.Request.Method))
			c.JSON(http.StatusNotFound, gin.H{"error": "Route not found"})
			c.Abort()
			span.SetStatus(codes.Error, "Route not found")
			return
		}

		// 记录和追踪成功匹配的路由
		span.SetAttributes(attribute.String("matched_target", targetRules[0].Target))
		span.SetStatus(codes.Ok, "Route matched successfully")
		logger.Info("Successfully matched route",
			zap.String("path", path),
			zap.Any("rules", targetRules))

		// 将追踪上下文传递下游并处理请求
		c.Request = c.Request.WithContext(ctx)
		httpProxy.createHTTPHandler(targetRules)(c)
	})
}
