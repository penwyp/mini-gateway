package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/security"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

type JWTAuthenticator struct {
	cfg *config.Config
}

func (j *JWTAuthenticator) Authenticate(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		logger.Warn("No Authorization header provided")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
		c.Abort()
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		logger.Warn("Invalid Authorization header format")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid Authorization header"})
		c.Abort()
		return
	}

	token := parts[1]
	claims, err := security.ValidateToken(token)
	if err != nil {
		logger.Warn("Invalid JWT token", zap.Error(err))
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
		c.Abort()
		return
	}

	c.Set("username", claims.Username)
	logger.Debug("JWT validated", zap.String("username", claims.Username))
	c.Next()
}
