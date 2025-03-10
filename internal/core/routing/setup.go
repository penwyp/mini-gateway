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

	for path := range rules {
		if isRegexPattern(path) && engine != "trie-regexp" && engine != "regexp" {
			logger.Error("路由引擎与正则表达式路径不兼容",
				zap.String("engine", engine),
				zap.String("path", path),
				zap.String("hint", "请使用 'trie-regexp' 或 'regexp' 引擎支持正则路由"))
			os.Exit(1)
		}
	}
}

// Setup 初始化路由引擎并设置路由规则
func Setup(r gin.IRouter, cfg *config.Config) {
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

	router.Setup(r, cfg)
}
