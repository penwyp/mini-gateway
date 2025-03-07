package routing

import (
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

// isRegexPattern 检查路径是否为正则表达式
func isRegexPattern(path string) bool {
	return strings.ContainsAny(path, ".*+?()|[]^$\\")
}

// validateRules 检查路由规则与引擎的兼容性
func validateRules(cfg *config.Config) {
	engine := cfg.Routing.Engine
	rules := cfg.Routing.Rules

	for path := range rules {
		if isRegexPattern(path) {
			// 如果路径是正则表达式，但引擎不支持，则报错
			if engine != "trie-regexp" && engine != "regexp" {
				logger.Error("Invalid routing engine for regex pattern",
					zap.String("engine", engine),
					zap.String("path", path),
					zap.String("hint", "Use 'trie-regexp' or 'regexp' for regex routes"),
				)
				os.Exit(1)
			}
		}
	}
}

// Setup 根据配置选择路由引擎并初始化
func Setup(r *gin.Engine, cfg *config.Config) {
	logger.Info("Routing rules loaded", zap.Any("rules", cfg.Routing.Rules))
	validateRules(cfg)

	var router Router
	switch cfg.Routing.Engine {
	case "trie":
		router = NewTrieRouter(cfg)
		logger.Info("Using Trie routing engine")
	case "trie-regexp":
		router = NewTrieRegexpRouter(cfg)
		logger.Info("Using Trie-Regexp routing engine")
	case "regexp":
		router = NewRegexpRouter(cfg)
		logger.Info("Using Regexp routing engine")
	case "gin":
		router = NewGinRouter(cfg)
		logger.Info("Using Gin routing engine")
	default:
		logger.Warn("Unknown routing engine, falling back to Gin",
			zap.String("engine", cfg.Routing.Engine),
		)
		router = NewGinRouter(cfg)
	}

	router.Setup(r, cfg)
}
