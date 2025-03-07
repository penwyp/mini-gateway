package security

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/casbin/casbin/v2"
	"github.com/denisbrodbeck/machineid"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

var enforcer *casbin.Enforcer
var tokenStore = make(map[string]string)

// InitRBAC 初始化 Casbin RBAC 规则
func InitRBAC(cfg *config.Config) {
	// 从 CSV 文件加载策略
	e, err := casbin.NewEnforcer(cfg.Security.RBAC.ModelPath, cfg.Security.RBAC.PolicyPath)
	if err != nil {
		logger.Error("Failed to initialize Casbin enforcer",
			zap.String("policyPath", cfg.Security.RBAC.PolicyPath),
			zap.Error(err),
		)
		panic(err)
	}
	enforcer = e

	// 调试加载的策略
	loadedPolicies, err := enforcer.GetPolicy()
	if err != nil {
		logger.Warn("Failed to get loaded policies", zap.Error(err))
	}
	logger.Info("RBAC initialized with Casbin",
		zap.Bool("enabled", cfg.Security.RBAC.Enabled),
		zap.String("modelPath", cfg.Security.RBAC.ModelPath),
		zap.String("policyPath", cfg.Security.RBAC.PolicyPath),
		zap.Any("loadedPolicies", loadedPolicies),
	)
}

// GenerateRBACLoginToken 生成基于机器 ID 的 RBAC 登录 token
func GenerateRBACLoginToken(username string) (string, error) {
	// 获取机器唯一 ID
	machineID, err := machineid.ProtectedID("mini-gateway")
	if err != nil {
		logger.Error("Failed to get machine ID", zap.Error(err))
		return "", err
	}

	// 组合 机器 ID + 用户名 + 时间戳
	rawToken := fmt.Sprintf("%s-%s-%d", machineID, username, time.Now().UnixNano())

	// 计算 SHA256 哈希
	hash := sha256.Sum256([]byte(rawToken))
	token := base64.URLEncoding.EncodeToString(hash[:])

	// 存储 Token
	tokenStore[token] = username
	logger.Debug("RBAC login token generated",
		zap.String("username", username),
		zap.String("token", token),
	)

	return token, nil
}

// ValidateRBACLoginToken 验证 RBAC 登录 token
func ValidateRBACLoginToken(token string) (string, bool) {
	username, exists := tokenStore[token]
	if !exists {
		logger.Warn("Invalid RBAC login token", zap.String("token", token))
		return "", false
	}
	return username, true
}

// CheckPermission 检查权限
func CheckPermission(sub, obj, act string) bool {
	if enforcer == nil {
		logger.Warn("Casbin enforcer not initialized")
		return false
	}
	ok, err := enforcer.Enforce(sub, obj, act)
	if err != nil {
		logger.Error("Failed to enforce RBAC policy",
			zap.String("subject", sub),
			zap.String("object", obj),
			zap.String("action", act),
			zap.Error(err),
		)
		return false
	}
	return ok
}
