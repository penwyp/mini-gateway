package routing

import (
	"github.com/penwyp/mini-gateway/config"
	"sync"
)

// objectPoolManager 管理可重用资源的对象池逻辑
type objectPoolManager struct {
	cfg *config.Config // 配置信息

	targetsPool sync.Pool // 目标地址切片池
	rulesPool   sync.Pool // 路由规则切片池
}

// newPoolManager 创建并初始化对象池管理器实例
func newPoolManager(cfg *config.Config) *objectPoolManager {
	pm := &objectPoolManager{
		cfg: cfg,
	}

	// 仅当配置中启用内存池时初始化池
	if cfg.Performance.MemoryPool.Enabled {
		pm.targetsPool = sync.Pool{
			New: func() interface{} {
				return make([]string, 0, cfg.Performance.MemoryPool.RulesCapacity)
			},
		}
		pm.rulesPool = sync.Pool{
			New: func() interface{} {
				return make(config.RoutingRules, 0, cfg.Performance.MemoryPool.TargetsCapacity)
			},
		}
	} else {
		// 未启用池时使用空池，避免空引用
		pm.targetsPool = sync.Pool{}
		pm.rulesPool = sync.Pool{}
	}
	return pm
}

// getTargets 从池中获取可重用的目标切片或创建新切片
func (pm *objectPoolManager) getTargets(capacity int) []string {
	if pm.cfg.Performance.MemoryPool.Enabled {
		targets := pm.targetsPool.Get().([]string)
		return targets[:0] // 重置长度以重用切片
	}
	return make([]string, 0, capacity) // 未启用池时分配新切片
}

// putTargets 将目标切片归还到池中以供重用
func (pm *objectPoolManager) putTargets(targets []string) {
	if pm.cfg.Performance.MemoryPool.Enabled {
		pm.targetsPool.Put(targets)
	}
	// 未启用池时，切片将通过垃圾回收自动丢弃
}

// getRules 从池中获取可重用的路由规则切片或创建新切片
func (pm *objectPoolManager) getRules(capacity int) config.RoutingRules {
	if pm.cfg.Performance.MemoryPool.Enabled {
		rules := pm.rulesPool.Get().(config.RoutingRules)
		return rules[:0] // 重置长度以重用切片
	}
	return make(config.RoutingRules, 0, capacity) // 未启用池时分配新切片
}

// putRules 将路由规则切片归还到池中以供重用
func (pm *objectPoolManager) putRules(rules config.RoutingRules) {
	if pm.cfg.Performance.MemoryPool.Enabled {
		pm.rulesPool.Put(rules)
	}
	// 未启用池时，切片将通过垃圾回收自动丢弃
}
