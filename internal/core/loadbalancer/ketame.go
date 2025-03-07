package loadbalancer

import (
	"crypto/md5"
	"encoding/binary"
	"net/http"
	"sort"
	"strconv"
)

type Ketama struct {
	nodes    []string
	hashRing []uint32
	hashMap  map[uint32]string
	replicas int
}

func NewKetama(replicas int) *Ketama {
	return &Ketama{
		replicas: replicas,
		hashMap:  make(map[uint32]string),
	}
}

func (k *Ketama) SelectTarget(targets []string, req *http.Request) string {
	if len(targets) == 0 {
		return ""
	}

	if len(k.nodes) != len(targets) {
		k.buildRing(targets)
	}

	if len(k.hashRing) == 0 {
		return targets[0]
	}

	key := k.hashKey(req.RemoteAddr) // 使用客户端 IP 作为哈希键
	index := k.findNearest(key)
	return k.hashMap[k.hashRing[index]]
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
