package routing

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.uber.org/zap"
)

// WebSocketPool 管理 WebSocket 连接池
type WebSocketPool struct {
	pool      map[string]*websocket.Conn // 目标地址到连接的映射
	mu        sync.RWMutex
	maxIdle   int           // 最大空闲连接数
	idleTime  time.Duration // 连接空闲超时时间
	dialer    *websocket.Dialer
	cleanupCh chan struct{}
}

// NewWebSocketPool 创建新的 WebSocket 连接池
func NewWebSocketPool(cfg *config.Config) *WebSocketPool {
	pool := &WebSocketPool{
		pool:      make(map[string]*websocket.Conn),
		maxIdle:   cfg.WebSocket.MaxIdleConns, // 从配置读取，默认为 10
		idleTime:  cfg.WebSocket.IdleTimeout,  // 从配置读取，默认为 5 分钟
		dialer:    websocket.DefaultDialer,
		cleanupCh: make(chan struct{}),
	}
	go pool.startCleanup() // 启动清理 goroutine
	return pool
}

// GetConn 获取或创建到目标的 WebSocket 连接
func (p *WebSocketPool) GetConn(target string) (*websocket.Conn, error) {
	p.mu.RLock()
	if conn, ok := p.pool[target]; ok && conn != nil {
		p.mu.RUnlock()
		return conn, nil
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	// 双重检查
	if conn, ok := p.pool[target]; ok && conn != nil {
		return conn, nil
	}

	// 创建新连接
	conn, resp, err := p.dialer.Dial(target, nil)
	if err != nil {
		logger.Error("WebSocket 连接失败",
			zap.String("target", target),
			zap.Error(err))
		if resp != nil {
			logger.Debug("WebSocket 响应状态", zap.Int("status", resp.StatusCode))
		}
		return nil, err
	}

	p.pool[target] = conn
	logger.Info("WebSocket 连接创建成功",
		zap.String("target", target))
	return conn, nil
}

// ReleaseConn 释放连接（实际上由清理 goroutine 处理）
func (p *WebSocketPool) ReleaseConn(target string) {
	// 这里不立即关闭连接，留给清理 goroutine 处理
}

// Close 关闭连接池并清理所有连接
func (p *WebSocketPool) Close() {
	close(p.cleanupCh)
	p.mu.Lock()
	defer p.mu.Unlock()
	for target, conn := range p.pool {
		if err := conn.Close(); err != nil {
			logger.Warn("关闭 WebSocket 连接失败",
				zap.String("target", target),
				zap.Error(err))
		}
		delete(p.pool, target)
	}
}

// startCleanup 定期清理空闲连接
func (p *WebSocketPool) startCleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-p.cleanupCh:
			return
		case <-ticker.C:
			p.mu.Lock()
			for target, conn := range p.pool {
				// 检查连接是否空闲超时（简单检查）
				if len(p.pool) > p.maxIdle {
					if err := conn.Close(); err != nil {
						logger.Warn("清理 WebSocket 连接失败",
							zap.String("target", target),
							zap.Error(err))
					}
					delete(p.pool, target)
					logger.Info("清理空闲 WebSocket 连接",
						zap.String("target", target))
				}
			}
			p.mu.Unlock()
		}
	}
}
