package routing

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

type GinRouter struct{}

func NewGinRouter() *GinRouter {
	return &GinRouter{}
}

func (gr *GinRouter) Setup(r *gin.Engine, cfg *config.Config) {
	rules := cfg.Routing.Rules
	if len(rules) == 0 {
		logger.Warn("No routing rules found in configuration")
		return
	}

	for path, target := range rules {
		targetURL, err := url.Parse(target)
		if err != nil {
			logger.Error("Failed to parse target URL",
				zap.String("path", path),
				zap.String("target", target),
				zap.Error(err),
			)
			continue
		}

		proxy := httputil.NewSingleHostReverseProxy(targetURL)
		proxy.Director = func(req *http.Request) {
			req.URL.Scheme = targetURL.Scheme
			req.URL.Host = targetURL.Host
			// 剥离原始路径的 "/api/v1" 前缀，只保留 "/user" 或 "/order" 后的部分
			originalPath := req.URL.Path
			trimmedPath := strings.TrimPrefix(originalPath, path) // path 是规则中的键，如 "/api/v1/user"
			req.URL.Path = singleJoiningSlash(targetURL.Path, trimmedPath)
			req.Host = targetURL.Host
			forwardedURL := req.URL.String()
			logger.Debug("Proxy forwarding",
				zap.String("original_path", originalPath),
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

		r.Any(path, func(c *gin.Context) {
			logger.Debug("Routing request",
				zap.String("path", c.Request.URL.Path),
				zap.String("target", target),
				zap.String("method", c.Request.Method),
			)
			proxy.ServeHTTP(c.Writer, c.Request)
		})

		logger.Info("Route registered in Gin",
			zap.String("path", path),
			zap.String("target", target),
		)
	}
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}
