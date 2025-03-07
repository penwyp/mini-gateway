package routing

import (
	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
)

// RegexpRouter 实现纯正则表达式路由引擎
type RegexpRouter struct {
	rules   map[string]*regexp.Regexp // 存储路径模式和正则对象的映射
	targets map[string]string         // 存储路径模式和目标地址的映射
}

// NewRegexpRouter 创建 RegexpRouter 实例
func NewRegexpRouter() *RegexpRouter {
	return &RegexpRouter{
		rules:   make(map[string]*regexp.Regexp),
		targets: make(map[string]string),
	}
}

// Setup 实现 Router 接口
func (rr *RegexpRouter) Setup(r *gin.Engine, cfg *config.Config) {
	rules := cfg.Routing.Rules
	if len(rules) == 0 {
		logger.Warn("No routing rules found in configuration")
	}

	// 加载路由规则并编译正则表达式
	for path, target := range rules {
		// 添加边界，确保全路径匹配
		pattern := "^" + path + "$"
		re, err := regexp.Compile(pattern)
		if err != nil {
			logger.Error("Failed to compile regex pattern",
				zap.String("path", path),
				zap.Error(err),
			)
			continue
		}
		rr.rules[path] = re
		rr.targets[path] = target
		logger.Info("Route registered in Regexp",
			zap.String("path", path),
			zap.String("target", target),
		)
	}

	// 使用中间件处理所有请求
	r.Use(func(c *gin.Context) {
		path := c.Request.URL.Path
		var target string
		var found bool

		// 遍历所有正则规则，查找匹配的路径
		for pattern, re := range rr.rules {
			if re.MatchString(path) {
				target = rr.targets[pattern]
				found = true
				break
			}
		}

		if !found {
			logger.Warn("Route not found",
				zap.String("path", path),
				zap.String("method", c.Request.Method),
			)
			c.JSON(http.StatusNotFound, gin.H{"error": "Route not found"})
			c.Abort()
			return
		}

		targetURL, err := url.Parse(target)
		if err != nil {
			logger.Error("Failed to parse target URL",
				zap.String("path", path),
				zap.String("target", target),
				zap.Error(err),
			)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid target URL"})
			c.Abort()
			return
		}

		proxy := httputil.NewSingleHostReverseProxy(targetURL)
		proxy.Director = func(req *http.Request) {
			req.URL.Scheme = targetURL.Scheme
			req.URL.Host = targetURL.Host
			req.URL.Path = singleJoiningSlash(targetURL.Path, req.URL.Path)
			req.Host = targetURL.Host
			forwardedURL := req.URL.String()
			logger.Debug("Proxy forwarding",
				zap.String("original_path", req.URL.Path),
				zap.String("forwarded_url", forwardedURL),
			)
		}
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error("Proxy error",
				zap.String("path", r.URL.Path),
				zap.String("target", target),
				zap.Error(err),
			)
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("Bad Gateway"))
		}

		logger.Debug("Routing request",
			zap.String("path", path),
			zap.String("target", target),
			zap.String("method", c.Request.Method),
		)
		proxy.ServeHTTP(c.Writer, c.Request)
	})
}
