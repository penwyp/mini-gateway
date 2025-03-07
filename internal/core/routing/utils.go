package routing

import (
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
	"net/http"
	"net/url"
	"strings"
)

// SingleJoiningSlash 合并路径，确保只有一个斜杠
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

func defaultDirector(targetURL *url.URL) func(req *http.Request) {
	return func(req *http.Request) {
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host
		req.URL.Path = SingleJoiningSlash(targetURL.Path, req.URL.Path)
		req.Host = targetURL.Host
		forwardedURL := req.URL.String()
		logger.Debug("Proxy forwarding",
			zap.String("original_path", req.URL.Path),
			zap.String("forwarded_url", forwardedURL),
		)
	}
}

func defaultErrorHandler(target string) func(w http.ResponseWriter, r *http.Request, err error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Error("Proxy error",
			zap.String("path", r.URL.Path),
			zap.String("target", target),
			zap.Error(err),
		)
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("Bad Gateway"))
	}
}
