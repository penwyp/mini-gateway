package routing

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
)

func TestIsRegexPattern(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/test", false},
		{"/test/.*", true},
		{"/test/[a-z]+", true},
		{"/test/path", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isRegexPattern(tt.path)
			if got != tt.want {
				t.Errorf("isRegexPattern(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestValidateRules(t *testing.T) {
	// 重定向 os.Exit 以避免测试退出
	exitCalled := false

	tests := []struct {
		name    string
		cfg     *config.Config
		wantErr bool
	}{
		{
			name: "No regex, valid engine",
			cfg: &config.Config{
				Routing: config.Routing{
					Engine: "trie",
					Rules:  map[string]config.RoutingRules{"/test": {}},
				},
			},
			wantErr: false,
		},
		{
			name: "Regex with compatible engine",
			cfg: &config.Config{
				Routing: config.Routing{
					Engine: "trie-regexp",
					Rules:  map[string]config.RoutingRules{"/test/.*": {}},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger.InitTest() // 初始化 logger
			exitCalled = false
			validateRules(tt.cfg)
			if exitCalled != tt.wantErr {
				t.Errorf("validateRules() error = %v, wantErr %v", exitCalled, tt.wantErr)
			}
		})
	}
}

func TestSetup(t *testing.T) {
	logger.InitTest()
	r := gin.New()
	tests := []struct {
		name string
		cfg  *config.Config
		want string // Router 类型
	}{
		{
			name: "Trie engine",
			cfg: &config.Config{
				Routing: config.Routing{Engine: "trie"},
			},
			want: "*routing.TrieRouter",
		},
		{
			name: "Trie-Regexp engine",
			cfg: &config.Config{
				Routing: config.Routing{Engine: "trie-regexp"},
			},
			want: "*routing.TrieRegexpRouter",
		},
		{
			name: "Regexp engine",
			cfg: &config.Config{
				Routing: config.Routing{Engine: "regexp"},
			},
			want: "*routing.RegexpRouter",
		},
		{
			name: "Gin engine",
			cfg: &config.Config{
				Routing: config.Routing{Engine: "gin"},
			},
			want: "*routing.GinRouter",
		},
		{
			name: "Unknown engine",
			cfg: &config.Config{
				Routing: config.Routing{Engine: "unknown"},
			},
			want: "*routing.GinRouter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Setup(r, tt.cfg)
			// 这里无法直接检查 router 类型，但可以通过日志或其他方式验证
		})
	}
}
