package routing

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/loadbalancer"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

type TrieRegexpRouter struct {
	Trie *TrieRegexp
	lb   loadbalancer.LoadBalancer
}

type TrieRegexp struct {
	Root *TrieRegexpNode
}

type TrieRegexpNode struct {
	Children     map[rune]*TrieRegexpNode
	Rules        config.RoutingRules
	IsEnd        bool
	Regex        *regexp.Regexp
	RegexPattern string
}

func NewTrieRegexpRouter(cfg *config.Config) *TrieRegexpRouter {
	lb, err := loadbalancer.NewLoadBalancer(cfg.Routing.LoadBalancer, cfg)
	if err != nil {
		logger.Error("创建负载均衡器失败", zap.Error(err))
		lb = loadbalancer.NewRoundRobin()
	}
	return &TrieRegexpRouter{
		Trie: &TrieRegexp{Root: &TrieRegexpNode{Children: make(map[rune]*TrieRegexpNode)}},
		lb:   lb,
	}
}

func (t *TrieRegexp) Insert(path string, rules config.RoutingRules) {
	node := t.Root
	path = strings.TrimPrefix(path, "/")

	if strings.ContainsAny(path, ".*+?()|[]^$\\") {
		re, err := regexp.Compile("^" + path + "$")
		if err != nil {
			logger.Error("无效的正则表达式模式",
				zap.String("path", "/"+path),
				zap.Error(err))
			return
		}
		node.Regex = re
		node.RegexPattern = path
		node.Rules = rules
		logger.Info("在 Trie 中插入正则路由",
			zap.String("pattern", "/"+path),
			zap.Any("rules", rules))
		return
	}

	for _, ch := range path {
		if node.Children[ch] == nil {
			node.Children[ch] = &TrieRegexpNode{Children: make(map[rune]*TrieRegexpNode)}
		}
		node = node.Children[ch]
	}
	node.Rules = rules
	node.IsEnd = true
	logger.Info("在 Trie 中插入路由",
		zap.String("path", "/"+path),
		zap.Any("rules", rules))
}

func (t *TrieRegexp) Search(path string) (config.RoutingRules, bool) {
	node := t.Root
	cleanPath := strings.TrimPrefix(path, "/")
	for _, ch := range cleanPath {
		if node.Children[ch] == nil {
			break
		}
		node = node.Children[ch]
	}
	if node != nil && node.IsEnd {
		return node.Rules, true
	}

	if t.Root.Regex != nil && t.Root.Regex.MatchString(path) {
		return t.Root.Rules, true
	}

	return nil, false
}

func (tr *TrieRegexpRouter) Setup(r gin.IRouter, cfg *config.Config) {
	rules := cfg.Routing.Rules
	if len(rules) == 0 {
		logger.Warn("配置中未找到路由规则")
		return
	}

	for path, targetRules := range rules {
		tr.Trie.Insert(path, targetRules)
	}

	r.Use(func(c *gin.Context) {
		path := c.Request.URL.Path
		targetRules, found := tr.Trie.Search(path)
		if !found {
			logger.Warn("路由未找到",
				zap.String("path", path),
				zap.String("method", c.Request.Method))
			c.JSON(http.StatusNotFound, gin.H{"error": "路由未找到"})
			c.Abort()
			return
		}

		createProxyHandler(targetRules, tr.lb)(c)
	})
}
