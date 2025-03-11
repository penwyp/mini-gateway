package auth

import (
	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
)

func Auth() gin.HandlerFunc {
	cfg := config.InitConfig()
	authenticator := NewAuthenticator(cfg)
	return func(c *gin.Context) {
		authenticator.Authenticate(c)
	}
}
