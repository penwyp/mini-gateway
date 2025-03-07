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

type GinRouter struct {
	lb loadbalancer.LoadBalancer
}

func NewGinRouter(cfg *config.Config) *GinRouter {
	lb, err := loadbalancer.NewLoadBalancer(cfg.Routing.LoadBalancer, cfg)
	if err != nil {
		logger.Error("Failed to create load balancer", zap.Error(err))
		lb = loadbalancer.NewRoundRobin()
	}
	return &GinRouter{lb: lb}
}

func (gr *GinRouter) Setup(r *gin.Engine, cfg *config.Config) {
	rules := cfg.Routing.Rules
	if len(rules) == 0 {
		logger.Warn("No routing rules found in configuration")
		return
	}

	for path, targetRules := range rules {
		targets := make([]string, len(targetRules))
		for i, rule := range targetRules {
			targets[i] = rule.Target
		}
		logger.Info("Route registered in Gin",
			zap.String("path", path),
			zap.Any("targets", targetRules),
		)
		r.Any(path, func(c *gin.Context) {
			target := gr.lb.SelectTarget(targets, c.Request)
			if target == "" {
				logger.Warn("No available targets",
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method),
				)
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "No available targets"})
				c.Abort()
				return
			}

			targetURL, err := url.Parse(target)
			if err != nil {
				logger.Error("Failed to parse target URL",
					zap.String("path", c.Request.URL.Path),
					zap.String("target", target),
					zap.Error(err),
				)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid target URL"})
				c.Abort()
				return
			}

			proxy := httputil.NewSingleHostReverseProxy(targetURL)
			proxy.Director = defaultDirector(targetURL)
			proxy.ErrorHandler = defaultErrorHandler(target)

			logger.Debug("Routing request",
				zap.String("path", c.Request.URL.Path),
				zap.String("target", target),
				zap.String("method", c.Request.Method),
			)
			proxy.ServeHTTP(c.Writer, c.Request)
		})
	}
}
