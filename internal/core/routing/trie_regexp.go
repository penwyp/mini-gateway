package routing

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

// TrieRegexpRouter 实现带正则支持的 Trie 路由
type TrieRegexpRouter struct {
	Trie *TrieRegexp
}

// TrieRegexp 表示支持正则的 Trie 树
type TrieRegexp struct {
	Root *TrieRegexpNode
	// 正则路由存储在根节点，优先级低于精确匹配
}

// TrieRegexpNode 表示 Trie 树节点，支持正则
type TrieRegexpNode struct {
	Children     map[rune]*TrieRegexpNode
	Target       string
	IsEnd        bool
	Regex        *regexp.Regexp // 正则表达式
	RegexPattern string         // 原始正则模式
}

// NewTrieRegexpRouter 创建 TrieRegexpRouter 实例
func NewTrieRegexpRouter() *TrieRegexpRouter {
	return &TrieRegexpRouter{
		Trie: &TrieRegexp{
			Root: &TrieRegexpNode{Children: make(map[rune]*TrieRegexpNode)},
		},
	}
}

// Insert 插入路由规则，支持正则表达式
func (t *TrieRegexp) Insert(path, target string) {
	node := t.Root
	path = strings.TrimPrefix(path, "/")

	// 检查是否为正则表达式路径
	if strings.ContainsAny(path, ".*+?()|[]^$\\") {
		re, err := regexp.Compile("^" + path + "$") // 添加边界，确保全匹配
		if err != nil {
			logger.Error("Invalid regex pattern",
				zap.String("path", "/"+path),
				zap.Error(err),
			)
			return
		}
		// 存储到根节点，支持多个正则路由
		node.Regex = re
		node.RegexPattern = path
		node.Target = target
		logger.Info("Regex route inserted into Trie",
			zap.String("pattern", "/"+path),
			zap.String("target", target),
		)
		return
	}

	// 普通路径插入
	for _, ch := range path {
		if node.Children[ch] == nil {
			node.Children[ch] = &TrieRegexpNode{Children: make(map[rune]*TrieRegexpNode)}
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

// Search 查找路径对应的目标地址，先精确匹配再正则匹配
func (t *TrieRegexp) Search(path string) (string, bool) {
	// 先尝试精确匹配
	node := t.Root
	cleanPath := strings.TrimPrefix(path, "/")
	for _, ch := range cleanPath {
		if node.Children[ch] == nil {
			break
		}
		node = node.Children[ch]
	}
	if node != nil && node.IsEnd {
		return node.Target, true
	}

	// 再尝试正则匹配（仅检查根节点的正则规则）
	if t.Root.Regex != nil && t.Root.Regex.MatchString(path) {
		return t.Root.Target, true
	}

	return "", false
}

// Setup 实现 Router 接口
func (tr *TrieRegexpRouter) Setup(r *gin.Engine, cfg *config.Config) {
	rules := cfg.Routing.Rules
	if len(rules) == 0 {
		logger.Warn("No routing rules found in configuration")
	}

	for path, target := range rules {
		tr.Trie.Insert(path, target)
	}

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
