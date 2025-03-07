package routing

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/loadbalancer"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

type RegexpRouter struct {
	rules map[string]*regexp.Regexp
	lb    loadbalancer.LoadBalancer
}

func NewRegexpRouter(cfg *config.Config) *RegexpRouter {
	lb, err := loadbalancer.NewLoadBalancer(cfg.Routing.LoadBalancer, cfg)
	if err != nil {
		logger.Error("Failed to create load balancer", zap.Error(err))
		lb = loadbalancer.NewRoundRobin()
	}
	return &RegexpRouter{
		rules: make(map[string]*regexp.Regexp),
		lb:    lb,
	}
}

func (rr *RegexpRouter) Setup(r gin.IRouter, cfg *config.Config) {
	rules := cfg.Routing.Rules
	if len(rules) == 0 {
		logger.Warn("No routing rules found in configuration")
	}

	for path, targetRules := range rules {
		pattern := "^" + path + "$"
		re, err := regexp.Compile(pattern)
		if err != nil {
			logger.Error("Failed to compile regex pattern",
				zap.String("path", path),
				zap.Error(err),
			)
			continue
		}
		targets := make([]string, len(targetRules))
		for i, rule := range targetRules {
			targets[i] = rule.Target
		}
		rr.rules[path] = re
		logger.Info("Route registered in Regexp",
			zap.String("path", path),
			zap.Any("targets", targetRules),
		)
	}

	r.Use(func(c *gin.Context) {
		path := c.Request.URL.Path
		var targets []string
		var found bool

		for pattern, re := range rr.rules {
			if re.MatchString(path) {
				targetRules := cfg.Routing.Rules[pattern]
				targets = make([]string, len(targetRules))
				for i, rule := range targetRules {
					targets[i] = rule.Target
				}
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

		target := rr.lb.SelectTarget(targets, c.Request)
		if target == "" {
			logger.Warn("No available targets",
				zap.String("path", path),
				zap.String("method", c.Request.Method),
			)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "No available targets"})
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
		proxy.Director = defaultDirector(targetURL)
		proxy.ErrorHandler = defaultErrorHandler(target)

		logger.Debug("Routing request",
			zap.String("path", path),
			zap.String("target", target),
			zap.String("method", c.Request.Method),
		)
		proxy.ServeHTTP(c.Writer, c.Request)
	})
}
