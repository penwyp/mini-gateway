package loadbalancer

import (
	"crypto/md5"
	"encoding/binary"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"net/http"
	"sort"
	"strconv"
	"sync" // 新增 sync 包
)

var kTracer = otel.Tracer("loadbalancer:ketama") // 定义负载均衡模块的 Tracer

type Ketama struct {
	nodes    []string
	hashRing []uint32
	hashMap  map[uint32]string
	replicas int
	mu       sync.RWMutex // 添加读写锁
}

func NewKetama(replicas int) *Ketama {
	return &Ketama{
		replicas: replicas,
		hashMap:  make(map[uint32]string),
		mu:       sync.RWMutex{}, // 初始化锁
	}
}

func (k *Ketama) SelectTarget(targets []string, req *http.Request) string {
	// 开始追踪负载均衡选择
	_, span := kTracer.Start(req.Context(), "LoadBalancer.Select",
		trace.WithAttributes(attribute.Int("target_count", len(targets))))
	defer span.End()

	if len(targets) == 0 {
		span.SetAttributes(attribute.String("result", "no targets"))
		return ""
	}

	k.mu.RLock()
	// 检查是否需要重建环
	needRebuild := len(k.nodes) != len(targets)
	if !needRebuild {
		for i, node := range k.nodes {
			if node != targets[i] {
				needRebuild = true
				break
			}
		}
	}
	k.mu.RUnlock()

	if needRebuild {
		k.mu.Lock()
		// 双重检查，避免重复构建
		if len(k.nodes) != len(targets) || !equalSlice(k.nodes, targets) {
			k.buildRing(targets)
		}
		k.mu.Unlock()
	}

	k.mu.RLock()
	defer k.mu.RUnlock()

	if len(k.hashRing) == 0 {
		target := targets[0]
		span.SetAttributes(attribute.String("selected_target", target))
		logger.Debug("负载均衡选择的目标", zap.String("target", target))

		return target
	}

	key := k.hashKey(req.RemoteAddr) // 使用客户端 IP 作为哈希键
	index := k.findNearest(key)

	target := k.hashMap[k.hashRing[index]]
	span.SetAttributes(attribute.String("selected_target", target))
	logger.Debug("负载均衡选择的目标", zap.String("target", target))

	return target
}

func (k *Ketama) buildRing(targets []string) {
	k.nodes = targets
	k.hashRing = nil
	k.hashMap = make(map[uint32]string)

	totalSlots := len(targets) * k.replicas
	k.hashRing = make([]uint32, 0, totalSlots)

	for _, node := range targets {
		for j := 0; j < k.replicas; j++ {
			hash := k.hash(node + "-" + strconv.Itoa(j))
			k.hashRing = append(k.hashRing, hash)
			k.hashMap[hash] = node
		}
	}

	sort.Slice(k.hashRing, func(i, j int) bool {
		return k.hashRing[i] < k.hashRing[j]
	})
}

func (k *Ketama) hash(key string) uint32 {
	h := md5.Sum([]byte(key))
	return binary.BigEndian.Uint32(h[0:4])
}

func (k *Ketama) hashKey(clientAddr string) uint32 {
	return k.hash(clientAddr)
}

func (k *Ketama) findNearest(hash uint32) int {
	index := sort.Search(len(k.hashRing), func(i int) bool {
		return k.hashRing[i] >= hash
	})
	if index == len(k.hashRing) {
		return 0
	}
	return index
}

// 辅助函数：比较两个字符串切片是否相等
func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
