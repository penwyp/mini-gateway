// internal/core/routing/trie.go
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

// TrieRouter 实现 Trie 树路由
type TrieRouter struct {
	Trie *Trie
}

// Trie 表示 Trie 树结构
type Trie struct {
	Root *TrieNode
}

// TrieNode 表示 Trie 树的一个节点
type TrieNode struct {
	Children map[rune]*TrieNode
	Target   string
	IsEnd    bool
}

// NewTrieRouter 创建 TrieRouter 实例
func NewTrieRouter() *TrieRouter {
	return &TrieRouter{
		Trie: &Trie{
			Root: &TrieNode{Children: make(map[rune]*TrieNode)},
		},
	}
}

// Insert 插入路由规则到 Trie 树
func (t *Trie) Insert(path, target string) {
	node := t.Root
	path = strings.TrimPrefix(path, "/")
	for _, ch := range path {
		if node.Children[ch] == nil {
			node.Children[ch] = &TrieNode{Children: make(map[rune]*TrieNode)}
		}
		node = node.Children[ch]
	}
	node.Target = target
	node.IsEnd = true
	logger.Info("Route inserted into Trie",
		zap.String("path", "/"+path),
		zap.String("target", target),
	)
}

// Search 查找路径对应的目标地址
func (t *Trie) Search(path string) (string, bool) {
	node := t.Root
	path = strings.TrimPrefix(path, "/")
	for _, ch := range path {
		if node.Children[ch] == nil {
			return "", false
		}
		node = node.Children[ch]
	}
	if node.IsEnd {
		return node.Target, true
	}
	return "", false
}

// Setup 实现 Router 接口，使用 Trie 树路由
func (tr *TrieRouter) Setup(r *gin.Engine, cfg *config.Config) {
	// 加载路由规则
	rules := cfg.Routing.Rules
	if len(rules) == 0 {
		logger.Warn("No routing rules found in configuration")
	}

	for path, target := range rules {
		tr.Trie.Insert(path, target)
	}

	// 注册全局中间件
	r.Use(func(c *gin.Context) {
		path := c.Request.URL.Path
		target, found := tr.Trie.Search(path)
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

		logger.Debug("Routing request",
			zap.String("path", path),
			zap.String("target", target),
			zap.String("method", c.Request.Method),
		)
		proxy.ServeHTTP(c.Writer, c.Request)
	})
}
