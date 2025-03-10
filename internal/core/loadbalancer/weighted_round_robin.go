package loadbalancer

import (
	"net/http"
	"sync"
)

// TargetWeight 定义目标及其权重
type TargetWeight struct {
	Target string
	Weight int
}

// WeightedRoundRobin 加权轮询负载均衡器
type WeightedRoundRobin struct {
	rules  map[string][]TargetWeight
	states map[string]*wrrState
	mu     sync.Mutex
}

// wrrState 存储加权轮询的状态
type wrrState struct {
	targets      []string // 目标列表
	weights      []int    // 权重列表
	totalWeight  int      // 总权重
	currentCount int      // 当前请求计数器
}

// NewWeightedRoundRobin 创建加权轮询实例
func NewWeightedRoundRobin(rules map[string][]TargetWeight) *WeightedRoundRobin {
	wrr := &WeightedRoundRobin{
		rules:  rules,
		states: make(map[string]*wrrState),
	}

	for path, targetRules := range rules {
		targets := make([]string, len(targetRules))
		weights := make([]int, len(targetRules))
		totalWeight := 0
		for i, rule := range targetRules {
			targets[i] = rule.Target
			weights[i] = rule.Weight
			totalWeight += rule.Weight
		}
		wrr.states[path] = &wrrState{
			targets:      targets,
			weights:      weights,
			totalWeight:  totalWeight,
			currentCount: -1,
		}
	}
	return wrr
}

// SelectTarget 选择目标，按照权重比例分配
func (wrr *WeightedRoundRobin) SelectTarget(targets []string, req *http.Request) string {
	wrr.mu.Lock()
	defer wrr.mu.Unlock()

	// 如果传入的目标为空，返回空
	if len(targets) == 0 {
		return ""
	}

	// 如果传入的目标只有一个，直接返回
	if len(targets) == 1 {
		return targets[0]
	}

	// 尝试获取预定义规则的状态
	path := req.URL.Path
	state, ok := wrr.states[path]
	if !ok || len(state.targets) == 0 {
		// 如果没有预定义规则，使用传入的目标列表（简单轮询）
		count := 0
		if state != nil {
			count = state.currentCount
			state.currentCount = (state.currentCount + 1) % len(targets)
		}
		return targets[count%len(targets)]
	}

	// 加权轮询算法（仅使用预定义的 state.targets）
	n := len(state.targets)
	if n == 0 {
		return ""
	}

	// 增加计数器并计算当前选择
	state.currentCount++
	current := state.currentCount % state.totalWeight
	cumulativeWeight := 0

	// 根据累积权重选择目标
	for i := 0; i < n; i++ {
		cumulativeWeight += state.weights[i]
		if current < cumulativeWeight {
			return state.targets[i]
		}
	}

	// 理论上不会到达这里，但作为回退返回第一个目标
	return state.targets[0]
}
