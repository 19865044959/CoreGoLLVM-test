package main

import (
	"sort"
	"sync/atomic"
)

// registry 使用零值包级变量；register 内部按需创建 map，
// 因此不依赖各文件的变量初始化顺序。
type codec struct {
	name   string
	factor int
	mark   *[16]byte
}

type registryTable struct {
	m   map[string]*codec
	seq []string
}

var (
	registry      registryTable
	registerCalls int32
)

// register 的返回值供 `var _ = register(...)` 使用。
func register(name string, factor int) string {
	if registry.m == nil {
		registry.m = make(map[string]*codec, 8)
	}
	atomic.AddInt32(&registerCalls, 1)
	mark := new([16]byte)
	for i := 0; i < len(mark); i++ {
		mark[i] = byte(factor + i)
	}
	registry.m[name] = &codec{name: name, factor: factor, mark: mark}
	registry.seq = append(registry.seq, name)
	return name
}

func codecNames() []string {
	names := make([]string, 0, len(registry.m))
	for n := range registry.m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func total(v int) int {
	sum := 0
	for _, c := range registry.m {
		sum += v * c.factor
	}
	return sum
}

func markChecksum() int {
	sum := 0
	for _, c := range registry.m {
		for i := 0; i < len(c.mark); i++ {
			sum += int(c.mark[i])
		}
	}
	return sum
}

var (
	registrySizeAtInit  = -1
	registerCallsAtInit int32
)

func init() {
	registrySizeAtInit = len(registry.m)
	registerCallsAtInit = atomic.LoadInt32(&registerCalls)
}
