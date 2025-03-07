package plugin

import "github.com/gin-gonic/gin"

type Plugin interface {
	Setup(r *gin.Engine) // 插件注册方法
	Name() string        // 插件名称
}
