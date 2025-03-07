package loadbalancer

import (
	"github.com/penwyp/mini-gateway/pkg/util"
	"net/http"
	"sync"
)

type TargetWeight struct {
	Target string
	Weight int
}

type WeightedRoundRobin struct {
	rules  map[string][]TargetWeight
	states map[string]*wrrState
	mu     sync.Mutex
}

type wrrState struct {
	targets   []string
	weights   []int
	current   int
	maxWeight int
	gcd       int
}

func NewWeightedRoundRobin(rules map[string][]TargetWeight) *WeightedRoundRobin {
	wrr := &WeightedRoundRobin{
		rules:  rules,
		states: make(map[string]*wrrState),
	}
	for path, targetRules := range rules {
		targets := make([]string, len(targetRules))
		weights := make([]int, len(targetRules))
		for i, rule := range targetRules {
			targets[i] = rule.Target
			weights[i] = rule.Weight
		}
		wrr.states[path] = &wrrState{
			targets:   targets,
			weights:   weights,
			current:   -1,
			maxWeight: util.Max(weights),
			gcd:       util.GCD(weights),
		}
	}
	return wrr
}

func (wrr *WeightedRoundRobin) SelectTarget(_ []string, req *http.Request) string {
	wrr.mu.Lock()
	defer wrr.mu.Unlock()

	path := req.URL.Path
	state, ok := wrr.states[path]
	if !ok || len(state.targets) == 0 {
		return ""
	}

	n := len(state.targets)
	state.current = (state.current + 1) % n
	if state.current == 0 {
		state.maxWeight -= state.gcd
		if state.maxWeight <= 0 {
			state.maxWeight = util.Max(state.weights)
		}
	}

	for i := 0; i < n; i++ {
		idx := (state.current + i) % n
		if state.weights[idx] >= state.maxWeight {
			state.current = idx
			return state.targets[idx]
		}
	}
	return state.targets[state.current]
}
