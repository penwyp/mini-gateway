package security

import (
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

// Claims 定义 JWT 的声明
type Claims struct {
	UserID string `json:"user_id"`
	jwt.StandardClaims
}

// GenerateToken 生成 JWT token
func GenerateToken(userID string) (string, error) {
	cfg := config.GetConfig()
	expirationTime := time.Now().Add(time.Duration(cfg.Security.JWT.ExpiresIn) * time.Second)

	claims := &Claims{
		UserID: userID,
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: expirationTime.Unix(),
			IssuedAt:  time.Now().Unix(),
			Issuer:    "mini-gateway",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(cfg.Security.JWT.Secret))
	if err != nil {
		logger.Error("Failed to generate JWT token",
			zap.String("user_id", userID),
			zap.Error(err),
		)
		return "", err
	}

	logger.Info("JWT token generated",
		zap.String("user_id", userID),
		zap.Int64("expires_at", expirationTime.Unix()),
	)
	return tokenString, nil
}
