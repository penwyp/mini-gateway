package routing

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// trieRegexpTracer 为 Trie-Regexp 路由模块初始化追踪器
var trieRegexpTracer = otel.Tracer("router:trie-regexp")

// TrieRegexpRouter 使用混合 Trie 和正则表达式方法管理 HTTP 路由
type TrieRegexpRouter struct {
	Trie *TrieRegexp // Trie 数据结构实例
}

// TrieRegexp 表示支持根节点正则表达式的 Trie 结构
type TrieRegexp struct {
	Root *TrieRegexpNode // Trie 的根节点
}

// TrieRegexpNode 表示 Trie 中的一个节点，支持子节点和正则规则
type TrieRegexpNode struct {
	Children     map[rune]*TrieRegexpNode // 子节点映射
	Rules        config.RoutingRules      // 路由规则
	IsEnd        bool                     // 标记此节点是否为静态路径的终点
	Regex        *regexp.Regexp           // 正则表达式（根节点使用）
	RegexPattern string                   // 原始正则模式，用于日志记录
}

// NewTrieRegexpRouter 创建并初始化 TrieRegexpRouter 实例
func NewTrieRegexpRouter(cfg *config.Config) *TrieRegexpRouter {
	return &TrieRegexpRouter{
		Trie: &TrieRegexp{
			Root: &TrieRegexpNode{Children: make(map[rune]*TrieRegexpNode)},
		},
	}
}

// Insert 将路径及其路由规则插入 Trie，根节点处理正则模式
func (t *TrieRegexp) Insert(path string, rules config.RoutingRules) {
	node := t.Root
	path = strings.TrimPrefix(path, "/") // 规范化路径，去除前导斜杠

	// 在根节点处理正则表达式路径
	if strings.ContainsAny(path, ".*+?()|[]^$\\") {
		re, err := regexp.Compile("^" + path + "$") // 为精确匹配添加锚点
		if err != nil {
			logger.Error("Failed to compile regular expression pattern",
				zap.String("path", "/"+path),
				zap.Error(err))
			return
		}
		node.Regex = re
		node.RegexPattern = path
		node.Rules = rules
		logger.Info("Successfully inserted regex route into Trie",
			zap.String("pattern", "/"+path),
			zap.Any("rules", rules))
		return
	}

	// 将静态路径插入 Trie 结构
	for _, ch := range path {
		if node.Children[ch] == nil {
			node.Children[ch] = &TrieRegexpNode{Children: make(map[rune]*TrieRegexpNode)}
		}
		node = node.Children[ch]
	}
	node.Rules = rules
	node.IsEnd = true
	logger.Info("Successfully inserted static route into TrieRegexp",
		zap.String("path", "/"+path),
		zap.Any("rules", rules))
}

// Search 在 Trie 中查找路径的路由规则，支持静态 Trie 和根节点正则匹配
func (t *TrieRegexp) Search(path string) (config.RoutingRules, bool) {
	node := t.Root
	cleanPath := strings.TrimPrefix(path, "/") // 规范化路径以进行 Trie 遍历

	// 遍历 Trie 进行静态路径匹配
	for _, ch := range cleanPath {
		if node.Children[ch] == nil {
			break
		}
		node = node.Children[ch]
	}
	if node != nil && node.IsEnd {
		return node.Rules, true
	}

	// 如果根节点定义了正则表达式，则回退到正则匹配
	if t.Root.Regex != nil && t.Root.Regex.MatchString(path) {
		return t.Root.Rules, true
	}

	return nil, false
}

// Setup 根据配置在 Gin 路由器中设置 TrieRegexpRouter 的 HTTP 路由规则
func (tr *TrieRegexpRouter) Setup(r gin.IRouter, cfg *config.Config) {
	rules := cfg.Routing.GetHTTPRules()
	if len(rules) == 0 {
		logger.Warn("No HTTP routing rules found in configuration")
		return
	}

	// 将所有路由规则插入 TrieRegexp
	for path, targetRules := range rules {
		tr.Trie.Insert(path, targetRules)
	}

	httpProxy := NewHTTPProxy(cfg)

	// 中间件：处理路由匹配和代理转发
	r.Use(func(c *gin.Context) {
		// 开始追踪路由匹配过程
		ctx, span := trieRegexpTracer.Start(c.Request.Context(), "Routing.Match",
			trace.WithAttributes(attribute.String("path", c.Request.URL.Path)))
		defer span.End()

		path := c.Request.URL.Path
		targetRules, found := tr.Trie.Search(path)
		if !found {
			logger.Warn("No matching route found",
				zap.String("path", path),
				zap.String("method", c.Request.Method))
			c.JSON(http.StatusNotFound, gin.H{"error": "Route not found"})
			c.Abort()
			span.SetStatus(codes.Error, "Route not found")
			return
		}

		// 记录和追踪成功匹配的路由
		span.SetAttributes(attribute.String("matched_target", targetRules[0].Target))
		span.SetStatus(codes.Ok, "Route matched successfully")
		logger.Info("Successfully matched route in TrieRegexp",
			zap.String("path", path),
			zap.Any("rules", targetRules))

		// 将追踪上下文传递下游并处理请求
		c.Request = c.Request.WithContext(ctx)
		httpProxy.createHTTPHandler(targetRules)(c)
	})
}
