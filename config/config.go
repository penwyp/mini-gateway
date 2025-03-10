package config

import (
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// Config 定义网关的配置结构体，新增 Logger 配置
type Config struct {
	Server        Server        `mapstructure:"server"`
	Routing       Routing       `mapstructure:"routing"`
	Security      Security      `mapstructure:"security"`
	Traffic       Traffic       `mapstructure:"traffic"`
	Observability Observability `mapstructure:"observability"`
	Plugins       []string      `mapstructure:"plugins"` // 插件列表
	Logger        Logger        `mapstructure:"logger"`  // 新增日志配置
	Cache         Redis         `mapstructure:"cache"`
	Middleware    Middleware    `mapstructure:"middleware"` // 新增中间件配置
}

type Middleware struct {
	RateLimit     bool `mapstructure:"rateLimit"`     // 限流中间件
	IPAcl         bool `mapstructure:"ipAcl"`         // IP ACL 中间件
	AntiInjection bool `mapstructure:"antiInjection"` // 防注入中间件
	Auth          bool `mapstructure:"auth"`          // 认证中间件
	Breaker       bool `mapstructure:"breaker"`       // 熔断器开关
}

type Redis struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type RoutingRules []RoutingRule
type RoutingRule struct {
	Target string `mapstructure:"target"`
	Weight int    `mapstructure:"weight"`
}

type Routing struct {
	Rules        map[string]RoutingRules `mapstructure:"rules"`
	Engine       string                  `mapstructure:"engine"`
	LoadBalancer string                  `mapstructure:"loadBalancer"`
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
	Prometheus struct {
		Enabled bool   `mapstructure:"enabled"` // 是否启用 Prometheus
		Path    string `mapstructure:"path"`    // 指标暴露路径
	} `mapstructure:"prometheus"`
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

	// Logger（新增）
	v.SetDefault("logger.level", "info")
	v.SetDefault("logger.filePath", "logs/gateway.log")
	v.SetDefault("logger.maxSize", 100)   // 100 MB
	v.SetDefault("logger.maxBackups", 10) // 保留 10 个备份
	v.SetDefault("logger.maxAge", 30)     // 保留 30 天
	v.SetDefault("logger.compress", true) // 压缩旧日志
}

// InitConfig 初始化配置（供 main 函数调用）
func InitConfig() *Config {
	// 默认配置文件路径
	defaultConfigPath := "config/config.yaml"

	// 检查环境变量中是否指定了配置文件路径
	if envPath := os.Getenv("GATEWAY_CONFIG_PATH"); envPath != "" {
		defaultConfigPath = envPath
	}

	// 确保配置文件路径是绝对路径
	absPath, err := filepath.Abs(defaultConfigPath)
	if err != nil {
		log.Fatalf("Failed to get absolute path: %v", err)
	}

	// 加载配置
	return LoadConfig(absPath)
}
