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

type RBACAuthenticator struct {
	cfg *config.Config
}

func (r *RBACAuthenticator) Authenticate(c *gin.Context) {
	if !r.cfg.Security.RBAC.Enabled {
		c.Next()
		return
	}

	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		logger.Warn("No Authorization header provided for RBAC")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
		c.Abort()
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		logger.Warn("Invalid Authorization header format for RBAC")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid Authorization header"})
		c.Abort()
		return
	}

	token := parts[1]
	username, valid := security.ValidateRBACLoginToken(token)
	if !valid {
		logger.Warn("Invalid RBAC token")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid rbac token"})
		c.Abort()
		return
	}

	c.Set("username", username)

	sub := username
	obj := c.Request.URL.Path
	act := c.Request.Method

	if !security.CheckPermission(sub, obj, act) {
		logger.Warn("RBAC permission denied",
			zap.String("subject", sub),
			zap.String("object", obj),
			zap.String("action", act),
		)
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		c.Abort()
		return
	}

	logger.Debug("RBAC permission granted",
		zap.String("subject", sub),
		zap.String("object", obj),
		zap.String("action", act),
	)
	c.Next()
}
