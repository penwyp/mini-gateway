package routing

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/loadbalancer"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

type TrieRouter struct {
	Trie *Trie
	lb   loadbalancer.LoadBalancer
}

type Trie struct {
	Root *TrieNode
}

type TrieNode struct {
	Children map[rune]*TrieNode
	Targets  []string
	IsEnd    bool
}

func NewTrieRouter(cfg *config.Config) *TrieRouter {
	lb, err := loadbalancer.NewLoadBalancer(cfg.Routing.LoadBalancer, cfg)
	if err != nil {
		logger.Error("Failed to create load balancer", zap.Error(err))
		lb = loadbalancer.NewRoundRobin()
	}
	return &TrieRouter{
		Trie: &Trie{
			Root: &TrieNode{Children: make(map[rune]*TrieNode)},
		},
		lb: lb,
	}
}

func (t *Trie) Insert(path string, targets []string) {
	node := t.Root
	path = strings.TrimPrefix(path, "/")
	for _, ch := range path {
		if node.Children[ch] == nil {
			node.Children[ch] = &TrieNode{Children: make(map[rune]*TrieNode)}
		}
		node = node.Children[ch]
	}
	node.Targets = targets
	node.IsEnd = true
	logger.Info("Route inserted into Trie",
		zap.String("path", "/"+path),
		zap.Strings("targets", targets),
	)
}

func (t *Trie) Search(path string) ([]string, bool) {
	node := t.Root
	path = strings.TrimPrefix(path, "/")
	for _, ch := range path {
		if node.Children[ch] == nil {
			return nil, false
		}
		node = node.Children[ch]
	}
	if node.IsEnd {
		return node.Targets, true
	}
	return nil, false
}

func (tr *TrieRouter) Setup(r gin.IRouter, cfg *config.Config) {
	rules := cfg.Routing.Rules
	if len(rules) == 0 {
		logger.Warn("No routing rules found in configuration")
	}

	for path, targetRules := range rules {
		targets := make([]string, len(targetRules))
		for i, rule := range targetRules {
			targets[i] = rule.Target
		}
		tr.Trie.Insert(path, targets)
		logger.Info("Route registered in Trie",
			zap.String("path", path),
			zap.Any("targets", targetRules),
		)
	}

	r.Use(func(c *gin.Context) {
		path := c.Request.URL.Path
		targets, found := tr.Trie.Search(path)
		if !found {
			logger.Warn("Route not found",
				zap.String("path", path),
				zap.String("method", c.Request.Method),
			)
			c.JSON(http.StatusNotFound, gin.H{"error": "Route not found"})
			c.Abort()
			return
		}

		target := tr.lb.SelectTarget(targets, c.Request)
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
