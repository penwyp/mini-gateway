package routing

import (
	"github.com/penwyp/mini-gateway/config"
	"testing"
)

func TestTrie(t *testing.T) {
	trie := &Trie{Root: &TrieNode{Children: make(map[rune]*TrieNode)}}
	rules := config.RoutingRules{
		{Target: "http://localhost:8081", Weight: 80, Env: "stable"},
	}
	trie.Insert("/api/v1/user", rules)

	// 测试完全匹配
	result, found := trie.Search("/api/v1/user")
	if !found || len(result) == 0 {
		t.Errorf("Expected to find rules for /api/v1/user, but got none")
	}

	// 测试部分匹配
	result, found = trie.Search("/api/v1/use")
	if found {
		t.Errorf("Expected no match for /api/v1/use, but found rules")
	}

	// 测试带斜杠的路径
	result, found = trie.Search("/api/v1/user/")
	if found {
		t.Errorf("Expected no match for /api/v1/user/, but found rules")
	}
}
