package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redismock/v9"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/cache"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"github.com/stretchr/testify/assert"
)

func TestCacheMiddleware(t *testing.T) {
	// 初始化日志（避免空指针）
	logger.InitTestLogger()

	// 创建 Redis mock
	client, mock := redismock.NewClientMock()
	cache.Client = client // 将 mock 客户端注入到 cache 包

	// 配置测试用例
	cfg := &config.Config{
		Cache: config.Cache{
			Addr: "localhost:6379", // mock 不需要真实的地址
		},
		Caching: config.Caching{
			Enabled: true,
			Rules: []config.CachingRule{
				{
					Path:      "/test",
					Method:    "GET",
					Threshold: 2,
					TTL:       1 * time.Minute,
				},
			},
		},
	}

	// 初始化 Gin
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CacheMiddleware(cfg))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "Hello, World!")
	})

	// 测试场景 1: 第一次请求，未达到阈值，无缓存
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	mock.ExpectIncr("req_count:/test").SetVal(1) // 模拟请求计数为 1
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "Hello, World!", w.Body.String())

	// 测试场景 2: 第二次请求，未达到阈值，无缓存
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/test", nil)
	mock.ExpectIncr("req_count:/test").SetVal(2) // 模拟请求计数为 2
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "Hello, World!", w.Body.String())

	// 测试场景 3: 第三次请求，达到阈值，设置缓存
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/test", nil)
	mock.ExpectIncr("req_count:/test").SetVal(3)                                   // 模拟请求计数为 3
	mock.ExpectSet("cache:GET:/test", "Hello, World!", 1*time.Minute).SetVal("OK") // 模拟缓存设置
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "Hello, World!", w.Body.String())

	// 测试场景 4: 第四次请求，命中缓存
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/test", nil)
	mock.ExpectGet("cache:GET:/test").SetVal("Hello, World!") // 模拟缓存命中
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "Hello, World!", w.Body.String())

	// 验证所有 mock 预期是否被满足
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}
