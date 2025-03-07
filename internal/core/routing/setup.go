package routing

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

// Setup 初始化路由模块，将配置中的路由规则注册到 Gin 引擎
func Setup(r *gin.Engine, cfg *config.Config) {
	// 获取路由规则
	rules := cfg.Routing.Rules
	if len(rules) == 0 {
		logger.Warn("No routing rules found in configuration")
		return
	}

	// 遍历并注册路由
	for path, target := range rules {
		// 解析目标 URL
		targetURL, err := url.Parse(target)
		if err != nil {
			logger.Error("Failed to parse target URL",
				zap.String("path", path),
				zap.String("target", target),
				zap.Error(err),
			)
			continue
		}

		// 创建反向代理
		proxy := httputil.NewSingleHostReverseProxy(targetURL)
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error("Proxy error",
				zap.String("path", r.URL.Path),
				zap.String("target", target),
				zap.Error(err),
			)
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("Bad Gateway"))
		}

		// 注册路由处理函数
		r.Any(path, func(c *gin.Context) {
			logger.Debug("Routing request",
				zap.String("path", c.Request.URL.Path),
				zap.String("target", target),
				zap.String("method", c.Request.Method),
			)
			proxy.ServeHTTP(c.Writer, c.Request)
		})

		// 记录成功注册的路由
		logger.Info("Route registered",
			zap.String("path", path),
			zap.String("target", target),
		)
	}
}
