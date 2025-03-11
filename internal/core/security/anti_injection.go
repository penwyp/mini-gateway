package security

import (
	"bytes"
	"fmt"
	"github.com/penwyp/mini-gateway/internal/core/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"io"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

var owaspTracer = otel.Tracer("anti:owasp") // 定义认证模块的 Tracer

// OWASP 正则规则库
var injectionPatterns = []*regexp.Regexp{
	// SQL 注入
	regexp.MustCompile(`(?i)(\b(union|select|insert|update|delete|drop|alter|create|truncate|exec|execute)\b)`),
	regexp.MustCompile(`(?i)(\b(from|into|where|having|join)\b)`),

	// XSS 注入
	regexp.MustCompile(`(?i)(<script|<iframe|<object|<embed|<svg|<img|on[a-z]+ ?=)`),
	regexp.MustCompile(`(?i)(javascript:|data:|vbscript:)`),

	// 命令注入
	regexp.MustCompile(`(?i)(\b(exec|system|eval|bash|sh|cmd|powershell)\b)`),

	// 文件路径注入
	regexp.MustCompile(`(?i)(\.\./|\.\./\.\./|\\/|\betc\b|\bpasswd\b)`),
}

// AntiInjection 中间件实现防注入检查
func AntiInjection() gin.HandlerFunc {
	return func(c *gin.Context) {
		_, span := owaspTracer.Start(c.Request.Context(), "Anti.Check",
			trace.WithAttributes(attribute.String("path", c.Request.URL.Path)))
		defer span.End()

		// 检查 Query 参数
		for key, values := range c.Request.URL.Query() {
			for _, value := range values {
				if isInjectionDetected(key) || isInjectionDetected(value) {
					logger.Warn("Injection detected in query",
						zap.String("key", key),
						zap.String("value", value),
						zap.String("ip", c.ClientIP()),
					)
					span.SetStatus(codes.Error, "Injection detected in query")
					observability.AntiInjectionBlocks.WithLabelValues(c.Request.URL.Path).Inc()
					c.JSON(http.StatusBadRequest, gin.H{"error": "Potential injection attack detected"})
					c.Abort()
					return
				}
			}
		}

		// 检查 Form 数据
		if err := c.Request.ParseForm(); err == nil {
			for key, values := range c.Request.Form {
				for _, value := range values {
					if isInjectionDetected(key) || isInjectionDetected(value) {
						logger.Warn("Injection detected in form",
							zap.String("key", key),
							zap.String("value", value),
							zap.String("ip", c.ClientIP()),
						)
						span.SetStatus(codes.Error, "Injection detected in form")
						observability.AntiInjectionBlocks.WithLabelValues(c.Request.URL.Path).Inc()
						c.JSON(http.StatusBadRequest, gin.H{"error": "Potential injection attack detected"})
						c.Abort()
						return
					}
				}
			}
		}

		// 检查 JSON Body
		if c.Request.Method == http.MethodPost || c.Request.Method == http.MethodPut {
			// 读取 Body
			bodyBytes, err := io.ReadAll(c.Request.Body)
			if err != nil {
				logger.Warn("Failed to read request body", zap.Error(err))
				c.Next()
				return
			}
			// 恢复 Body 以供后续使用
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			// 检查 JSON
			var jsonBody map[string]interface{}
			if err := c.BindJSON(&jsonBody); err == nil {
				for key, value := range jsonBody {
					if isInjectionDetected(key) || isInjectionDetected(fmt.Sprintf("%v", value)) {
						logger.Warn("Injection detected in JSON body",
							zap.String("key", key),
							zap.Any("value", value),
							zap.String("ip", c.ClientIP()),
						)
						span.SetStatus(codes.Error, "Injection detected in JSON body")
						observability.AntiInjectionBlocks.WithLabelValues(c.Request.URL.Path).Inc()
						c.JSON(http.StatusBadRequest, gin.H{"error": "Potential injection attack detected"})
						c.Abort()
						return
					}
				}
			}
			// 再次恢复 Body（BindJSON 可能会再次消耗）
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}

		// 检查 Header
		for key, values := range c.Request.Header {
			for _, value := range values {
				if isInjectionDetected(key) || isInjectionDetected(value) {
					logger.Warn("Injection detected in header",
						zap.String("key", key),
						zap.String("value", value),
						zap.String("ip", c.ClientIP()),
					)

					span.SetStatus(codes.Error, "Injection detected in header")
					observability.AntiInjectionBlocks.WithLabelValues(c.Request.URL.Path).Inc()
					c.JSON(http.StatusBadRequest, gin.H{"error": "Potential injection attack detected"})
					c.Abort()
					return
				}
			}
		}

		span.SetStatus(codes.Ok, "Request processed")
		c.Next()
	}
}

// isInjectionDetected 检查是否匹配注入模式
func isInjectionDetected(input string) bool {
	for _, pattern := range injectionPatterns {
		if pattern.MatchString(input) {
			return true
		}
	}
	return false
}
