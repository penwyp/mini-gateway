package routing

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/health"
	"github.com/penwyp/mini-gateway/internal/core/loadbalancer"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"github.com/valyala/fasthttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const (
	defaultEnv = "stable" // 默认环境
	canaryEnv  = "canary" // 金丝雀环境
)

// httpTracer 为 HTTP 代理初始化追踪器
var httpTracer = otel.Tracer("proxy:http")

// HTTPProxy 管理 HTTP 代理功能
type HTTPProxy struct {
	httpPool        *HTTPConnectionPool       // HTTP 连接池
	loadBalancer    loadbalancer.LoadBalancer // 负载均衡器
	objectPool      *objectPoolManager        // 对象池管理器
	httpPoolEnabled bool                      // 是否启用 HTTP 连接池
}

// NewHTTPProxy 创建并初始化 HTTPProxy 实例
func NewHTTPProxy(cfg *config.Config) *HTTPProxy {
	lb := initializeLoadBalancer(cfg)
	logPoolStatus(cfg.Performance.HttpPoolEnabled)

	return &HTTPProxy{
		httpPool:        NewHTTPConnectionPool(cfg),
		loadBalancer:    lb,
		objectPool:      newPoolManager(cfg),
		httpPoolEnabled: cfg.Performance.HttpPoolEnabled,
	}
}

// SetupHTTPProxy 配置 HTTP 代理路由
func (hp *HTTPProxy) SetupHTTPProxy(r gin.IRouter, cfg *config.Config) {
	rules := cfg.Routing.GetHTTPRules()
	if len(rules) == 0 {
		logger.Warn("No HTTP routing rules found in configuration")
		return
	}

	for path, targetRules := range rules {
		logger.Info("Configuring HTTP proxy route",
			zap.String("path", path),
			zap.Any("targets", targetRules))
		r.Any(path, hp.createHTTPHandler(targetRules))
	}
}

// createHTTPHandler 创建 HTTP 请求处理函数
func (hp *HTTPProxy) createHTTPHandler(rules config.RoutingRules) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, span := httpTracer.Start(c.Request.Context(), "HTTPProxy.Handle",
			trace.WithAttributes(
				attribute.String("http.method", c.Request.Method),
				attribute.String("http.path", c.Request.URL.Path),
			))
		defer span.End()

		c.Request = c.Request.WithContext(ctx)
		env := getEnvFromHeader(c)

		filteredRules := hp.filterRules(rules, env)
		defer hp.objectPool.putRules(filteredRules)

		targets := hp.extractTargets(filteredRules)
		defer hp.objectPool.putTargets(targets)

		target, selectedEnv := hp.selectTarget(c, targets, filteredRules)
		if target == "" {
			handleNoTarget(c, span, c.Request.URL.Path, env)
			return
		}

		span.SetAttributes(attribute.String("proxy.target", target))
		if hp.httpPoolEnabled {
			hp.proxyWithPool(c, target, selectedEnv, span)
		} else {
			hp.proxyDirect(c, target, selectedEnv, span)
		}
	}
}

// proxyDirect 使用直接代理方式转发请求
func (hp *HTTPProxy) proxyDirect(c *gin.Context, target, env string, span trace.Span) {
	targetURL, err := url.Parse(target)
	if err != nil {
		handleProxyError(c, span, target, "Invalid target URL", err)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Director = hp.createDirector(targetURL, env)
	proxy.ErrorHandler = hp.createErrorHandler(target, span)

	logger.Info("Routing HTTP request",
		zap.String("path", c.Request.URL.Path),
		zap.String("target", target),
		zap.String("env", env),
		zap.String("method", c.Request.Method))

	proxy.ServeHTTP(c.Writer, c.Request)
	span.SetStatus(codes.Ok, "HTTP proxy completed successfully")
	health.GetGlobalHealthChecker().UpdateRequestCount(target, true)
}

// proxyWithPool 使用连接池代理转发请求
func (hp *HTTPProxy) proxyWithPool(c *gin.Context, target, env string, span trace.Span) {
	client := hp.httpPool.GetClient(target)
	req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	hp.prepareFastHTTPRequest(c, req, target, env)

	if err := client.Do(req, resp); err != nil {
		handleProxyError(c, span, target, "Backend service unavailable", err)
		return
	}

	hp.writeFastHTTPResponse(c, resp)
	span.SetStatus(codes.Ok, "HTTP proxy completed successfully")
	health.GetGlobalHealthChecker().UpdateRequestCount(target, true)
}

// initializeLoadBalancer 初始化负载均衡器
func initializeLoadBalancer(cfg *config.Config) loadbalancer.LoadBalancer {
	lb, err := loadbalancer.NewLoadBalancer(cfg.Routing.LoadBalancer, cfg)
	if err != nil {
		logger.Error("Failed to initialize load balancer",
			zap.Error(err))
		return loadbalancer.NewRoundRobin()
	}
	return lb
}

// logPoolStatus 记录连接池状态
func logPoolStatus(enabled bool) {
	status := "disabled"
	if enabled {
		status = "enabled"
	}
	logger.Info("HTTP TCP connection pool status",
		zap.String("status", status))
}

// getEnvFromHeader 从请求头中获取环境信息
func getEnvFromHeader(c *gin.Context) string {
	if env := c.GetHeader("X-Env"); env != "" {
		return env
	}
	return defaultEnv
}

// filterRules 根据环境过滤路由规则
func (hp *HTTPProxy) filterRules(rules config.RoutingRules, env string) config.RoutingRules {
	filtered := hp.objectPool.getRules(len(rules))
	if env == canaryEnv {
		for _, rule := range rules {
			if rule.Env == canaryEnv {
				filtered = append(filtered, rule)
			}
		}
		if len(filtered) == 0 {
			logger.Warn("No canary targets available, falling back to all rules",
				zap.String("path", rules[0].Target)) // 假设 rules 不为空
			return rules
		}
		return filtered
	}
	return append(filtered, rules...)
}

// extractTargets 从规则中提取目标列表
func (hp *HTTPProxy) extractTargets(rules config.RoutingRules) []string {
	targets := hp.objectPool.getTargets(len(rules))
	for _, rule := range rules {
		targets = append(targets, rule.Target)
	}
	return targets
}

// selectTarget 选择目标并返回目标和环境
func (hp *HTTPProxy) selectTarget(c *gin.Context, targets []string, rules config.RoutingRules) (string, string) {
	target := hp.loadBalancer.SelectTarget(targets, c.Request)
	if target == "" {
		return "", ""
	}
	for _, rule := range rules {
		if rule.Target == target {
			return target, rule.Env
		}
	}
	return target, defaultEnv
}

// handleNoTarget 处理无可用目标的情况
func handleNoTarget(c *gin.Context, span trace.Span, path, env string) {
	span.SetStatus(codes.Error, "No available target")
	logger.Warn("No target available for request",
		zap.String("path", path),
		zap.String("env", env))
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "No available target"})
}

// handleProxyError 处理代理错误
func handleProxyError(c *gin.Context, span trace.Span, target, msg string, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, "Proxy error")
	health.GetGlobalHealthChecker().UpdateRequestCount(target, false)
	logger.Error("HTTP proxy request failed",
		zap.String("target", target),
		zap.String("message", msg),
		zap.Error(err))
	c.JSON(http.StatusBadGateway, gin.H{"error": msg})
}

// createDirector 创建代理请求的 Director 函数
func (hp *HTTPProxy) createDirector(targetURL *url.URL, env string) func(*http.Request) {
	return func(req *http.Request) {
		defaultDirector(targetURL)(req)
		if env == canaryEnv {
			req.Header.Set("X-Env", canaryEnv)
		}
	}
}

// createErrorHandler 创建代理错误处理函数
func (hp *HTTPProxy) createErrorHandler(target string, span trace.Span) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Proxy error")
		health.GetGlobalHealthChecker().UpdateRequestCount(target, false)
		logger.Error("HTTP proxy request failed",
			zap.String("path", r.URL.Path),
			zap.String("target", target),
			zap.Error(err))
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("Bad Gateway"))
	}
}

// prepareFastHTTPRequest 准备 FastHTTP 请求
func (hp *HTTPProxy) prepareFastHTTPRequest(c *gin.Context, req *fasthttp.Request, target, env string) {
	reqURI := "http://" + target + c.Request.URL.Path
	if c.Request.URL.RawQuery != "" {
		reqURI += "?" + c.Request.URL.RawQuery
	}
	req.SetRequestURI(reqURI)
	req.Header.SetMethod(c.Request.Method)

	for key, values := range c.Request.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if env == canaryEnv {
		req.Header.Set("X-Env", canaryEnv)
	}
	if c.Request.Body != nil {
		if body, err := c.GetRawData(); err == nil {
			req.SetBody(body)
		}
	}
}

// writeFastHTTPResponse 写入 FastHTTP 响应
func (hp *HTTPProxy) writeFastHTTPResponse(c *gin.Context, resp *fasthttp.Response) {
	c.Status(resp.StatusCode())
	resp.Header.VisitAll(func(key, value []byte) {
		c.Header(string(key), string(value))
	})
	c.Writer.Write(resp.Body())
}
