package security

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

var jwtSecret string

// Claims 自定义 JWT Claims
type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// InitJWT 初始化 JWT 配置
func InitJWT(cfg *config.Config) {
	jwtSecret = cfg.Security.JWT.Secret
	if jwtSecret == "" {
		logger.Warn("JWT secret not set, using default")
		jwtSecret = "default-secret-key" // 仅用于测试，生产环境需配置强密钥
	}
}

// GenerateToken 生成 JWT token
func GenerateToken(username string) (string, error) {
	cfg := config.InitConfig()
	if jwtSecret == "" {
		InitJWT(cfg)
	}

	expirationTime := time.Now().Add(time.Duration(cfg.Security.JWT.ExpiresIn) * time.Second)
	claims := &Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   username,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(jwtSecret))
}

// ValidateToken 验证 JWT token
func ValidateToken(tokenString string) (*Claims, error) {
	cfg := config.InitConfig()
	if jwtSecret == "" {
		InitJWT(cfg)
	}

	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(jwtSecret), nil
	})

	if err != nil {
		logger.Warn("Failed to parse JWT token", zap.Error(err))
		return nil, err
	}

	if !token.Valid {
		logger.Warn("Invalid JWT token")
		return nil, fmt.Errorf("invalid jwt token")
	}

	logger.Debug("JWT token validated",
		zap.String("username", claims.Username),
		zap.Time("expires", claims.ExpiresAt.Time),
	)
	return claims, nil
}
