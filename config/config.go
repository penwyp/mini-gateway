package config

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// Config 定义网关的配置结构体，新增 Logger 配置
type Config struct {
	Server        Server          `mapstructure:"server"`
	Routing       Routing         `mapstructure:"routing"`
	Security      Security        `mapstructure:"security"`
	Traffic       Traffic         `mapstructure:"traffic"`
	Observability Observability   `mapstructure:"observability"`
	Plugins       []string        `mapstructure:"plugins"` // 插件列表
	Logger        Logger          `mapstructure:"logger"`  // 新增日志配置
	Cache         Redis           `mapstructure:"cache"`
	Consul        Consul          `mapstructure:"consul"`
	Middleware    Middleware      `mapstructure:"middleware"` // 新增中间件配置
	GRPC          GRPCConfig      `mapstructure:"grpc"`
	WebSocket     WebSocketConfig `mapstructure:"websocket"` // 新增
}

type Consul struct {
	Enabled bool   `mapstructure:"enabled"`
	Addr    string `mapstructure:"addr"`
}

// WebSocketConfig WebSocket 配置
type WebSocketConfig struct {
	Enabled      bool          `mapstructure:"enabled"`
	MaxIdleConns int           `mapstructure:"maxIdleConns"`
	IdleTimeout  time.Duration `mapstructure:"idleTimeout"`
	Prefix       string        `mapstructure:"prefix"` // 新增字段
}

// GetWebSocketRules 获取 WebSocket 路由规则
func (r Routing) GetWebSocketRules() map[string]RoutingRules {
	wsRules := make(map[string]RoutingRules)
	for path, rules := range r.Rules {
		var filteredRules RoutingRules
		for _, rule := range rules {
			if rule.Protocol == "websocket" {
				filteredRules = append(filteredRules, rule)
			}
		}
		if len(filteredRules) > 0 {
			wsRules[path] = filteredRules
		}
	}
	return wsRules
}

type GRPCConfig struct {
	Enabled         bool     `mapstructure:"enabled"`
	Prefix          string   `mapstructure:"prefix"` // 新增字段
	HealthCheckPath string   `mapstructure:"healthCheckPath"`
	Reflection      bool     `mapstructure:"reflection"`
	AllowedOrigins  []string `mapstructure:"allowedOrigins"`
}

type Middleware struct {
	RateLimit     bool `mapstructure:"rateLimit"`     // 限流中间件
	IPAcl         bool `mapstructure:"ipAcl"`         // IP ACL 中间件
	AntiInjection bool `mapstructure:"antiInjection"` // 防注入中间件
	Auth          bool `mapstructure:"auth"`          // 认证中间件
	Breaker       bool `mapstructure:"breaker"`       // 熔断器开关
	Tracing       bool `mapstructure:"tracing"`       // OpenTelemetry 中间件
}

type Redis struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type RoutingRule struct {
	Target   string `mapstructure:"target"`
	Weight   int    `mapstructure:"weight"`
	Env      string `mapstructure:"env"`
	Protocol string `mapstructure:"protocol"` // 支持 "http", "grpc", "websocket"
}

type RoutingRules []RoutingRule

func (i RoutingRules) HasGrpcRule() bool {
	for _, rule := range i {
		if rule.Protocol == "grpc" {
			return true
		}
	}
	return false
}

func (i RoutingRules) HasWebsocketRule() bool {
	for _, rule := range i {
		if rule.Protocol == "websocket" {
			return true
		}
	}
	return false
}

type Routing struct {
	Rules        map[string]RoutingRules `mapstructure:"rules"`
	Engine       string                  `mapstructure:"engine"`
	LoadBalancer string                  `mapstructure:"loadBalancer"`
}

func (i Routing) GetGrpcRules() map[string]RoutingRules {
	grpcRules := make(map[string]RoutingRules)
	for path, rules := range i.Rules {
		if rules.HasGrpcRule() {
			grpcRules[path] = rules
		}
	}
	return grpcRules

}

func (i Routing) GetHTTPRules() map[string]RoutingRules {
	httpRules := make(map[string]RoutingRules)
	for path, rules := range i.Rules {
		if !rules.HasGrpcRule() && !rules.HasWebsocketRule() {
			httpRules[path] = rules
		}
	}
	return httpRules
}

type Server struct {
	Port string `mapstructure:"port"` // 服务监听端口
}

type JWT struct {
	Secret    string `mapstructure:"secret"`    // JWT 密钥
	ExpiresIn int    `mapstructure:"expiresIn"` // JWT 过期时间（秒）
	Enabled   bool   `mapstructure:"enabled"`   // 是否启用 JWT
}

type Security struct {
	AuthMode     string   `mapstructure:"authMode"` // 认证模式（如：JWT、OAuth2 等）
	JWT          JWT      `mapstructure:"jwt"`
	RBAC         RBAC     `mapstructure:"rbac"`
	IPBlacklist  []string `mapstructure:"ipBlacklist"`  // IP 黑名单
	IPWhitelist  []string `mapstructure:"ipWhitelist"`  // IP 白名单
	IPUpdateMode string   `mapstructure:"ipUpdateMode"` // 新增：覆盖（override）或追加（append）
}

type RBAC struct {
	Enabled    bool   `mapstructure:"enabled"`
	ModelPath  string `mapstructure:"modelPath"`  // RBAC 模型文件路径
	PolicyPath string `mapstructure:"policyPath"` // RBAC 策略文件路径
}

type TrafficRateLimit struct {
	Enabled   bool   `mapstructure:"enabled"`   // 是否启用限流
	QPS       int    `mapstructure:"qps"`       // 每秒请求数限制
	Burst     int    `mapstructure:"burst"`     // 令牌桶突发容量
	Algorithm string `mapstructure:"algorithm"` // 限流算法: "token_bucket" 或 "leaky_bucket"
}

type TrafficBreaker struct {
	Enabled        bool    `mapstructure:"enabled"`
	ErrorRate      float64 `mapstructure:"errorRate"`
	Timeout        int     `mapstructure:"timeout"`        // 毫秒
	MinRequests    int     `mapstructure:"minRequests"`    // 最小请求数
	SleepWindow    int     `mapstructure:"sleepWindow"`    // 毫秒
	MaxConcurrent  int     `mapstructure:"maxConcurrent"`  // 最大并发数
	WindowSize     int     `mapstructure:"windowSize"`     // 滑动窗口请求数
	WindowDuration int     `mapstructure:"windowDuration"` // 滑动窗口时间（秒）
}

type Traffic struct {
	RateLimit TrafficRateLimit `mapstructure:"rateLimit"`
	Breaker   TrafficBreaker   `mapstructure:"breaker"`
}

type Observability struct {
	Prometheus Prometheus   `mapstructure:"prometheus"`
	Jaeger     JaegerConfig `mapstructure:"jaeger"`
}

type Prometheus struct {
	Enabled bool   `mapstructure:"enabled"` // 是否启用 Prometheus
	Path    string `mapstructure:"path"`    // 指标暴露路径
}

type JaegerConfig struct {
	Enabled     bool    `mapstructure:"enabled"`
	Endpoint    string  `mapstructure:"endpoint"`
	Sampler     string  `mapstructure:"sampler"`     // "always" 或 "ratio"
	SampleRatio float64 `mapstructure:"sampleRatio"` // 采样比例
}

type Logger struct {
	Level      string `mapstructure:"level"`      // 日志级别 (debug, info, warn, error)
	FilePath   string `mapstructure:"filePath"`   // 日志文件路径
	MaxSize    int    `mapstructure:"maxSize"`    // 单个日志文件最大大小 (MB)
	MaxBackups int    `mapstructure:"maxBackups"` // 保留的旧日志文件数
	MaxAge     int    `mapstructure:"maxAge"`     // 日志文件保留天数
	Compress   bool   `mapstructure:"compress"`   // 是否压缩旧日志文件
}

// configInstance 用于存储全局配置实例
var (
	configInstance *Config
	configMutex    sync.RWMutex
)

// LoadConfig 加载配置文件并返回 Config 实例
func LoadConfig(configPath string) *Config {
	v := viper.New()

	// 设置配置文件路径和名称
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")

	// 设置默认值
	setDefaultValues(v)

	// 读取配置文件
	if err := v.ReadInConfig(); err != nil {
		log.Fatalf("Failed to read config file: %v", err)
	}

	// 解析配置到结构体
	config := &Config{}
	if err := v.Unmarshal(config); err != nil {
		log.Fatalf("Failed to unmarshal config: %v", err)
	}

	// 存储全局配置实例
	configMutex.Lock()
	configInstance = config
	configMutex.Unlock()

	// 监听配置文件变化，实现热更新
	v.WatchConfig()
	v.OnConfigChange(func(e fsnotify.Event) {
		log.Printf("Config file changed: %s", e.Name)
		if err := v.Unmarshal(config); err != nil {
			log.Printf("Failed to reload config: %v", err)
			return
		}
		configMutex.Lock()
		configInstance = config
		configMutex.Unlock()
		log.Println("Config reloaded successfully")
	})

	return config
}

// GetConfig 获取当前全局配置实例（线程安全）
func GetConfig() *Config {
	configMutex.RLock()
	defer configMutex.RUnlock()
	return configInstance
}

// setDefaultValues 设置默认配置值，包括新增的 Logger 配置
func setDefaultValues(v *viper.Viper) {
	// Server
	v.SetDefault("server.port", "8080")

	// Routing
	v.SetDefault("routing.engine", "gin")               // 默认使用 Gin 路由
	v.SetDefault("routing.loadBalancer", "round-robin") // 默认轮询

	// 中间件默认启用
	v.SetDefault("middleware.rateLimit", true)
	v.SetDefault("middleware.ipAcl", true)
	v.SetDefault("middleware.antiInjection", true)
	v.SetDefault("middleware.auth", true)
	v.SetDefault("middleware.breaker", true)

	// Cache
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)

	// Consul
	v.SetDefault("consul.enabled", false)
	v.SetDefault("consul.addr", "localhost:8500")

	// Security
	v.SetDefault("security.jwt.secret", "default-secret-key")
	v.SetDefault("security.jwt.expiresIn", 3600) // 默认 1 小时
	v.SetDefault("security.authMode", "none")    // 默认无认证
	v.SetDefault("security.rbac.enabled", false)
	v.SetDefault("security.rbac.modelPath", "config/data/rbac_model.conf")
	v.SetDefault("security.rbac.policyPath", "config/data/rbac_policy.csv")
	v.SetDefault("security.ipUpdateMode", "override") // 默认覆盖更新

	// Traffic
	v.SetDefault("traffic.rateLimit.enabled", true)
	v.SetDefault("traffic.rateLimit.qps", 1000)
	v.SetDefault("traffic.rateLimit.burst", 2000)
	v.SetDefault("traffic.rateLimit.algorithm", "token_bucket") // 默认使用令牌桶
	v.SetDefault("traffic.breaker.enabled", true)
	v.SetDefault("traffic.breaker.errorRate", 0.5)
	v.SetDefault("traffic.breaker.timeout", 1000)
	v.SetDefault("traffic.breaker.minRequests", 20)
	v.SetDefault("traffic.breaker.sleepWindow", 5000)
	v.SetDefault("traffic.breaker.maxConcurrent", 100)
	v.SetDefault("traffic.breaker.windowSize", 100)
	v.SetDefault("traffic.breaker.windowDuration", 10)

	// Observability
	v.SetDefault("observability.prometheus.enabled", true)
	v.SetDefault("observability.prometheus.path", "/metrics")
	v.SetDefault("observability.jaeger.enabled", false)
	v.SetDefault("observability.jaeger.endpoint", "http://localhost:14268/api/traces")
	v.SetDefault("observability.jaeger.sampler", "always")
	v.SetDefault("observability.jaeger.sampleRatio", 1.0)

	// Logger（新增）
	v.SetDefault("logger.level", "info")
	v.SetDefault("logger.filePath", "logs/gateway.log")
	v.SetDefault("logger.maxSize", 100)   // 100 MB
	v.SetDefault("logger.maxBackups", 10) // 保留 10 个备份
	v.SetDefault("logger.maxAge", 30)     // 保留 30 天
	v.SetDefault("logger.compress", true) // 压缩旧日志

	// GRPC
	v.SetDefault("grpc.enabled", true)
	v.SetDefault("grpc.healthCheckPath", "/grpc/health")
	v.SetDefault("grpc.reflection", false)
	v.SetDefault("grpc.allowedOrigins", []string{"*"})
	v.SetDefault("grpc.prefix", "/grpc") // 设置默认值

	// WebSocket
	v.SetDefault("websocket.enabled", true)
	v.SetDefault("websocket.maxIdleConns", 100)
	v.SetDefault("websocket.idleTimeout", 60*time.Second)
	v.SetDefault("websocket.prefix", "/websocket") // 设置默认值
}

// InitConfig 初始化配置（供 main 函数调用）
func InitConfig() *Config {
	cfg := &Config{}
	viper.SetConfigFile("config/config.yaml")
	if err := viper.ReadInConfig(); err != nil {
		logger.Error("读取配置文件失败", zap.Error(err))
		os.Exit(1)
	}
	if err := viper.Unmarshal(cfg); err != nil {
		logger.Error("解析配置文件失败", zap.Error(err))
		os.Exit(1)
	}

	// gRPC 配置检查
	if err := validateGRPCConfig(cfg); err != nil {
		logger.Error("gRPC 配置检查失败", zap.Error(err))
		os.Exit(1)
	}
	// WebSocket 配置检查
	if err := validateWebSocketConfig(cfg); err != nil {
		logger.Error("WebSocket 配置检查失败", zap.Error(err))
		os.Exit(1)
	}

	configInstance = cfg
	return cfg
}

func validateWebSocketConfig(cfg *Config) error {
	if cfg.WebSocket.Enabled {
		// 检查前缀
		if cfg.WebSocket.Prefix == "" || len(cfg.WebSocket.Prefix) < 5 {
			return fmt.Errorf("WebSocket 前缀为空或过短: %s", cfg.WebSocket.Prefix)
		}
		if strings.ContainsAny(cfg.WebSocket.Prefix, "..*?") {
			return fmt.Errorf("WebSocket 前缀包含非法字符: %s", cfg.WebSocket.Prefix)
		}

		// 检查路由规则
		wsRules := cfg.Routing.GetWebSocketRules()
		if len(wsRules) == 0 {
			return fmt.Errorf("WebSocket 已启用但未配置任何 WebSocket 路由规则")
		}

		// 检查连接池参数
		if cfg.WebSocket.MaxIdleConns < 0 {
			return fmt.Errorf("WebSocket maxIdleConns 不能为负数: %d", cfg.WebSocket.MaxIdleConns)
		}
		if cfg.WebSocket.IdleTimeout <= 0 {
			return fmt.Errorf("WebSocket idleTimeout 必须为正值: %s", cfg.WebSocket.IdleTimeout)
		}

		// 检查目标 URL
		for path, rules := range wsRules {
			for _, rule := range rules {
				if !strings.HasPrefix(rule.Target, "ws://") && !strings.HasPrefix(rule.Target, "wss://") {
					return fmt.Errorf("WebSocket 路由 %s 的目标 %s 必须以 ws:// 或 wss:// 开头", path, rule.Target)
				}
				if _, err := url.Parse(rule.Target); err != nil {
					return fmt.Errorf("WebSocket 路由 %s 的目标 %s 格式无效: %v", path, rule.Target, err)
				}
			}
		}
	}
	return nil
}

func validateGRPCConfig(cfg *Config) error {
	// 检查前缀
	if cfg.GRPC.Enabled {
		if cfg.GRPC.Prefix == "" || len(cfg.GRPC.Prefix) < 5 {
			return fmt.Errorf("gRPC 前缀为空或过短: %s", cfg.GRPC.Prefix)
		}
		if strings.ContainsAny(cfg.GRPC.Prefix, "..*?") {
			return fmt.Errorf("gRPC 前缀包含非法字符: %s", cfg.GRPC.Prefix)
		}

		// 检查路由规则
		grpcRules := cfg.Routing.GetGrpcRules()
		if len(grpcRules) == 0 {
			return fmt.Errorf("gRPC 已启用但未配置任何 gRPC 路由规则")
		}

		// 检查允许来源
		for _, origin := range cfg.GRPC.AllowedOrigins {
			if origin == "*" {
				logger.Warn("gRPC 允许所有来源，可能存在安全风险")
			} else if _, err := url.Parse(origin); err != nil {
				return fmt.Errorf("gRPC allowedOrigins 包含无效 URL: %s", origin)
			}
		}
	}
	return nil
}
