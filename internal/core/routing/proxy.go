package routing

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/loadbalancer"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

// createProxyHandler 创建代理处理器，支持流量染色和灰度发布
func createProxyHandler(rules config.RoutingRules, lb loadbalancer.LoadBalancer) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取染色 Header，默认 stable
		env := c.GetHeader("X-Env")
		if env == "" {
			env = "stable"
		}

		// 根据染色过滤规则
		var filteredRules config.RoutingRules
		if env == "canary" {
			for _, rule := range rules {
				if rule.Env == "canary" {
					filteredRules = append(filteredRules, rule)
				}
			}
			if len(filteredRules) == 0 {
				logger.Warn("未找到 canary 目标，降级到所有规则",
					zap.String("path", c.Request.URL.Path))
				filteredRules = rules
			}
		} else {
			filteredRules = rules
		}

		// 将规则转换为目标列表，供负载均衡器使用
		targets := make([]string, len(filteredRules))
		for i, rule := range filteredRules {
			targets[i] = rule.Target
		}

		// 使用负载均衡器选择目标
		target := lb.SelectTarget(targets, c.Request)
		if target == "" {
			logger.Warn("无可用目标",
				zap.String("path", c.Request.URL.Path),
				zap.String("env", env))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "无可用目标"})
			return
		}

		// 查找选择的规则以获取 Env
		var selectedEnv string
		for _, rule := range filteredRules {
			if rule.Target == target {
				selectedEnv = rule.Env
				break
			}
		}

		// 解析目标 URL
		targetURL, err := url.Parse(target)
		if err != nil {
			logger.Error("解析目标 URL 失败",
				zap.String("path", c.Request.URL.Path),
				zap.String("target", target),
				zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "无效的目标 URL"})
			return
		}

		// 创建反向代理
		proxy := httputil.NewSingleHostReverseProxy(targetURL)
		proxy.Director = func(req *http.Request) {
			defaultDirector(targetURL)(req)
			// 如果路由到 canary，注入 X-Env: canary
			if selectedEnv == "canary" {
				req.Header.Set("X-Env", "canary")
			}
		}
		proxy.ErrorHandler = defaultErrorHandler(target)

		// 执行代理并记录日志
		logger.Info("路由请求",
			zap.String("path", c.Request.URL.Path),
			zap.String("target", target),
			zap.String("env", selectedEnv),
			zap.String("method", c.Request.Method))
		proxy.ServeHTTP(c.Writer, c.Request)
	}
}
