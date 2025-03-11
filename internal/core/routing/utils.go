package routing

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

// SingleJoiningSlash 合并路径，确保只有一个斜杠连接
func SingleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

// defaultDirector 创建默认的代理请求导演函数
func defaultDirector(targetURL *url.URL) func(req *http.Request) {
	return func(req *http.Request) {
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host
		req.URL.Path = SingleJoiningSlash(targetURL.Path, req.URL.Path)
		req.Host = targetURL.Host
		forwardedURL := req.URL.String()
		logger.Debug("代理转发请求",
			zap.String("original_path", req.URL.Path),
			zap.String("forwarded_url", forwardedURL),
		)
	}
}
