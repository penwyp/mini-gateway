package routing

import (
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

// Router 定义路由引擎接口
type Router interface {
	Setup(r gin.IRouter, cfg *config.Config)
}

// isRegexPattern 检查路径是否为正则表达式
func isRegexPattern(path string) bool {
	return strings.ContainsAny(path, ".*+?()|[]^$\\")
}

// validateRules 检查路由规则与引擎的兼容性
func validateRules(cfg *config.Config) {
	engine := cfg.Routing.Engine
	rules := cfg.Routing.Rules

	for path, pathEndpoints := range rules {
		var shouldContinue bool
		for _, endpoint := range pathEndpoints {
			if endpoint.Protocol == "grpc" {
				shouldContinue = true
				break
			}
			if engine == "trie" && isRegexPattern(path) {
				logger.Error("Trie 路由引擎不支持正则表达式路径",
					zap.String("path", path),
					zap.String("hint", "请使用 'trie-regexp' 或 'regexp' 引擎支持正则路由"))
				os.Exit(1)
			}
		}
		if shouldContinue {
			continue
		}
		if isRegexPattern(path) && engine != "trie-regexp" && engine != "regexp" {
			logger.Error("路由引擎与正则表达式路径不兼容",
				zap.String("engine", engine),
				zap.String("path", path),
				zap.String("hint", "请使用 'trie-regexp' 或 'regexp' 引擎支持正则路由"))
			os.Exit(1)
		}
	}
}

// Setup 初始化路由引擎并设置路由规则，包括 gRPC 代理
func Setup(protected gin.IRouter, cfg *config.Config) {
	logger.Info("加载路由规则", zap.Any("rules", cfg.Routing.Rules))
	validateRules(cfg)

	var router Router
	switch cfg.Routing.Engine {
	case "trie":
		router = NewTrieRouter(cfg)
		logger.Info("使用 Trie 路由引擎")
	case "trie-regexp", "trie_regexp":
		router = NewTrieRegexpRouter(cfg)
		logger.Info("使用 Trie-Regexp 路由引擎")
	case "regexp":
		router = NewRegexpRouter(cfg)
		logger.Info("使用 Regexp 路由引擎")
	case "gin":
		router = NewGinRouter(cfg)
		logger.Info("使用 Gin 路由引擎")
	default:
		logger.Warn("未知的路由引擎，回退到 Gin",
			zap.String("engine", cfg.Routing.Engine))
		router = NewGinRouter(cfg)
	}

	grpcGroup := protected.Group("/grpc")

	// 设置 HTTP 路由
	router.Setup(protected, cfg)

	// 设置 grpc 路由
	if cfg.GRPC.Enabled && len(cfg.Routing.GetGrpcRules()) > 0 {
		// gRPC 代理需要访问底层的 *gin.Engine，因为它需要挂载独立的路由
		SetupGRPCProxy(cfg, grpcGroup) // HTTP 到 gRPC
		//SetupGRPCProxy(cfg, protected) // HTTP 到 gRPC
	}

	switch cfg.Routing.Engine {
	case "trie", "trie_regexp", "regexp":
		// 为所有动态路由注册一个空处理器，交给具体 Router 处理
		for p := range cfg.Routing.GetHTTPRules() {
			protected.Any(p, func(c *gin.Context) {}) // 空处理器，依赖 TrieRouter 中间件
		}
	}
}
