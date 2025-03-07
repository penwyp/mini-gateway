package loadbalancer

import (
	"net/http"
	"sync/atomic"
)

// RoundRobin 实现轮询负载均衡
type RoundRobin struct {
	counter uint32
}

// NewRoundRobin 创建轮询实例
func NewRoundRobin() *RoundRobin {
	return &RoundRobin{counter: 0}
}

// SelectTarget 选择目标
func (rr *RoundRobin) SelectTarget(targets []string, req *http.Request) string {
	if len(targets) == 0 {
		return ""
	}
	count := atomic.AddUint32(&rr.counter, 1)
	index := (count - 1) % uint32(len(targets))
	return targets[index]
}
