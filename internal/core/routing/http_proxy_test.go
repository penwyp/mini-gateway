package routing

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/internal/core/loadbalancer"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// mockLoadBalancer simulates a load balancer for testing purposes.
type mockLoadBalancer struct{}

// SelectTarget always returns the first target from the provided list, if available.
func (m *mockLoadBalancer) SelectTarget(targets []string, req *http.Request) string {
	if len(targets) > 0 {
		return targets[0]
	}
	return ""
}

// setupGinContext prepares a gin.Context and ResponseRecorder for testing.
func setupGinContext(method, path string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest(method, path, nil)
	c.Request = req
	return c, w
}

// BenchmarkCreateProxyHandler benchmarks the performance of createProxyHandler.
func BenchmarkCreateProxyHandler(b *testing.B) {
	gin.SetMode(gin.ReleaseMode)

	// Predefined routing rules for testing.
	rules := buildTestRules()

	// Initialize a mock load balancer.
	lb := &mockLoadBalancer{}

	// Test cases for benchmarking with memory pool enabled/disabled.
	tests := []struct {
		name    string
		enabled bool // Indicates whether MemoryPool is enabled.
	}{
		{"PoolEnabled", true},
		{"PoolDisabled", false},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			// Configure the gateway with routing rules and performance settings.
			cfg := &config.Config{
				Routing: config.Routing{
					Rules: map[string]config.RoutingRules{
						"/api/test": rules,
					},
				},
				Performance: config.Performance{
					MemoryPool: config.MemoryPool{
						Enabled:         tt.enabled,
						TargetsCapacity: 500,
						RulesCapacity:   500,
					},
				},
			}

			// Create the proxy handler with the specified configuration.
			handler := createProxyHandlerWithConfig(rules, lb, cfg)

			// Reset the timer to exclude setup time from the benchmark.
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				c, _ := setupGinContext("GET", "/api/test")
				if i%2 == 0 {
					c.Request.Header.Set("X-Env", "canary") // Simulate a canary request.
				}
				handler(c)
			}
		})
	}
}

// buildTestRules constructs a set of routing rules for testing.
// 中文说明：此函数生成一组测试用的路由规则，包含 stable 和 canary 环境的目标地址。
func buildTestRules() config.RoutingRules {
	return config.RoutingRules{
		{Target: "http://localhost:8080", Env: "stable"},
		{Target: "http://localhost:18080", Env: "stable"},
		{Target: "http://localhost:28080", Env: "stable"},
		{Target: "http://localhost:38080", Env: "stable"},
		{Target: "http://localhost:48080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:58080", Env: "stable"},
		{Target: "http://localhost:8081", Env: "canary"},
		{Target: "http://localhost:18081", Env: "canary"},
		{Target: "http://localhost:28081", Env: "canary"},
		{Target: "http://localhost:38081", Env: "canary"},
		{Target: "http://localhost:48081", Env: "canary"},
		{Target: "http://localhost:48081", Env: "canary"},
		{Target: "http://localhost:48081", Env: "canary"},
		{Target: "http://localhost:48081", Env: "canary"},
		{Target: "http://localhost:48081", Env: "canary"},
		{Target: "http://localhost:48081", Env: "canary"},
		{Target: "http://localhost:48081", Env: "canary"},
		{Target: "http://localhost:48081", Env: "canary"},
		{Target: "http://localhost:48081", Env: "canary"},
		{Target: "http://localhost:48081", Env: "canary"},
		{Target: "http://localhost:48081", Env: "canary"},
		{Target: "http://localhost:48081", Env: "canary"},
		{Target: "http://localhost:48081", Env: "canary"},
		{Target: "http://localhost:48081", Env: "canary"},
		{Target: "http://localhost:58081", Env: "canary"},
	}
}

// createProxyHandlerWithConfig creates a Gin handler for proxying requests with the given config.
// 中文说明：此函数根据配置生成一个代理请求的处理器，支持环境筛选和负载均衡。
func createProxyHandlerWithConfig(rules config.RoutingRules, lb loadbalancer.LoadBalancer, cfg *config.Config) gin.HandlerFunc {
	pm := newPoolManager(cfg)
	return func(c *gin.Context) {
		// Start tracing the request with OpenTelemetry.
		ctx, span := httpTracer.Start(c.Request.Context(), "HTTPProxy.Handle",
			trace.WithAttributes(
				attribute.String("http.method", c.Request.Method),
				attribute.String("http.path", c.Request.URL.Path),
			))
		defer span.End()

		// Attach the tracing context to the request.
		c.Request = c.Request.WithContext(ctx)

		// Determine the environment, defaulting to "stable" if not specified.
		env := c.GetHeader("X-Env")
		if env == "" {
			env = "stable"
		}

		// Retrieve a slice from the pool to store filtered rules.
		filteredRules := pm.getRules(len(rules))
		defer pm.putRules(filteredRules)

		// Filter rules based on the environment.
		if env == "canary" {
			for _, rule := range rules {
				if rule.Env == "canary" {
					filteredRules = append(filteredRules, rule)
				}
			}
			if len(filteredRules) == 0 {
				filteredRules = append(filteredRules, rules...) // Fallback to all rules.
			}
		} else {
			filteredRules = append(filteredRules, rules...)
		}

		// Retrieve a slice from the pool to store target addresses.
		targets := pm.getTargets(len(filteredRules))
		defer pm.putTargets(targets)

		// Extract target addresses from the filtered rules.
		for _, rule := range filteredRules {
			targets = append(targets, rule.Target)
		}

		// Select a target using the load balancer.
		target := lb.SelectTarget(targets, c.Request)
		if target == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "No available targets"})
			return
		}

		// Respond with the selected target (simplified for testing).
		c.String(http.StatusOK, "Proxied to "+target)
	}
}
