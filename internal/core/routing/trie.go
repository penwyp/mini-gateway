package routing

import (
	"net/http"
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
	Rules    config.RoutingRules
	IsEnd    bool
}

func NewTrieRouter(cfg *config.Config) *TrieRouter {
	lb, err := loadbalancer.NewLoadBalancer(cfg.Routing.LoadBalancer, cfg)
	if err != nil {
		logger.Error("创建负载均衡器失败", zap.Error(err))
		lb = loadbalancer.NewRoundRobin()
	}
	return &TrieRouter{
		Trie: &Trie{Root: &TrieNode{Children: make(map[rune]*TrieNode)}},
		lb:   lb,
	}
}

func (t *Trie) Insert(path string, rules config.RoutingRules) {
	node := t.Root
	path = strings.TrimPrefix(path, "/")
	for _, ch := range path {
		if node.Children[ch] == nil {
			node.Children[ch] = &TrieNode{Children: make(map[rune]*TrieNode)}
		}
		node = node.Children[ch]
	}
	node.Rules = rules
	node.IsEnd = true
	logger.Info("在 Trie 中插入路由",
		zap.String("path", "/"+path),
		zap.Any("rules", rules))
}

func (t *Trie) Search(path string) (config.RoutingRules, bool) {
	node := t.Root
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/") // 去掉末尾斜杠
	for _, ch := range path {
		if node.Children[ch] == nil {
			return nil, false
		}
		node = node.Children[ch]
	}
	if node.IsEnd {
		return node.Rules, true
	}
	return nil, false
}

func (tr *TrieRouter) Setup(r gin.IRouter, cfg *config.Config) {
	rules := cfg.Routing.GetHTTPRules()
	if len(rules) == 0 {
		logger.Warn("配置中未找到路由规则")
		return
	}

	for path, targetRules := range rules {
		tr.Trie.Insert(path, targetRules)
	}
	logger.Info("Trie 路由注册完成", zap.Int("rule_count", len(rules)))

	r.Use(func(c *gin.Context) {
		logger.Debug("进入 Trie 路由中间件", zap.String("path", c.Request.URL.Path))
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
		logger.Info("路由匹配成功", zap.String("path", path), zap.Any("rules", targetRules))
		createProxyHandler(targetRules, tr.lb)(c)
	})
}
