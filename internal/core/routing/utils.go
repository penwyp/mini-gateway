package routing

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

// SingleJoiningSlash 合并两个路径段，确保它们之间恰好有一个斜杠
func SingleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/") // 检查 a 是否以斜杠结尾
	bslash := strings.HasPrefix(b, "/") // 检查 b 是否以斜杠开头
	switch {
	case aslash && bslash:
		// 如果 a 已带斜杠且 b 以斜杠开头，移除 b 的前导斜杠
		return a + b[1:]
	case !aslash && !bslash:
		// 如果两者均无斜杠，添加一个斜杠
		return a + "/" + b
	}
	// 如果两者之间已有一个斜杠，直接拼接
	return a + b
}

// defaultDirector 创建默认的代理请求 Director 函数，用于将请求转发到目标 URL
func defaultDirector(targetURL *url.URL) func(req *http.Request) {
	return func(req *http.Request) {
		req.URL.Scheme = targetURL.Scheme                               // 设置目标协议
		req.URL.Host = targetURL.Host                                   // 设置目标主机
		req.URL.Path = SingleJoiningSlash(targetURL.Path, req.URL.Path) // 合并路径
		req.Host = targetURL.Host                                       // 设置 Host 头
		forwardedURL := req.URL.String()                                // 获取完整转发 URL
		logger.Debug("Forwarding proxy request",
			zap.String("originalPath", req.URL.Path),
			zap.String("forwardedURL", forwardedURL),
		)
	}
}
