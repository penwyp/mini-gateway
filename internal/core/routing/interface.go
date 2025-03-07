package routing

import (
	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
)

// Router 定义路由引擎接口
type Router interface {
	Setup(r gin.IRouter, cfg *config.Config)
}
