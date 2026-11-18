// L15: tags + GOOS/GOARCH 分区。unix 元标签与 64/32 位架构集合、windows 组合成
// 三个真实分区，第四个文件是三者并集的严格补集；构建成功即证明分区无交叠，
// 运行期用 init 标记确认恰好命中一个，并在 TORTURE_ROUNDS 轮分配中反复校验
// 标签选择的实现常数与 GC 后的对象字段。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type archNode struct {
	id    int64
	class string
	val   int64
}

var (
	failed     int32
	gcSink     [][]byte
	classInits []string
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func isUnixOS(goos string) bool {
	switch goos {
	case "aix", "android", "darwin", "dragonfly", "freebsd",
		"hurd", "illumos", "ios", "linux", "netbsd", "openbsd", "solaris":
		return true
	}
	return false
}

func is64(goarch string) bool {
	switch goarch {
	case "amd64", "arm64", "ppc64", "ppc64le", "mips64", "mips64le", "riscv64", "s390x", "loong64":
		return true
	}
	return false
}

func is32(goarch string) bool {
	switch goarch {
	case "386", "arm", "mips", "mipsle", "ppc", "s390":
		return true
	}
	return false
}

func expectedClass() (string, int64) {
	if runtime.GOOS == "windows" {
		return "windows", 4
	}
	if isUnixOS(runtime.GOOS) {
		if is64(runtime.GOARCH) {
			return "unix64", 3
		}
		if is32(runtime.GOARCH) {
			return "unix32", 2
		}
	}
	return "other", 1
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}

func main() {
	cls, scale := expectedClass()
	p := &archNode{id: 41, class: cls, val: 410}

	ck(platformClass() == cls, "GOOS/GOARCH partition selects class "+cls)
	ck(platformScale() == scale, "partition selects scale constant")
	ck(len(classInits) == 1 && classInits[0] == cls, "exactly one partition init marker")

	rounds := envInt("TORTURE_ROUNDS", 5000)
	seed := envInt("UAF_SEED", 1)
	delta := int64(seed % 5)

	ring := make([]*archNode, 512)
	filled := 0
	var sum int64
	gcEvery := rounds / 16
	if gcEvery < 1 {
		gcEvery = 1
	}

	for r := 0; r < rounds; r++ {
		n := &archNode{id: int64(r), class: platformClass(), val: int64(r)*scale + delta}
		ring[r%len(ring)] = n
		if filled < len(ring) {
			filled++
		}
		sum += n.val
		if r%64 == 0 {
			b := make([]byte, 2048)
			b[0] = byte(r)
			gcSink = append(gcSink[:0], b)
		}
		if r%gcEvery == 0 {
			runtime.GC()
		}
	}

	wantSum := scale*int64(rounds)*int64(rounds-1)/2 + int64(rounds)*delta
	ck(sum == wantSum, fmt.Sprintf("round checksum mismatch seed=%d rounds=%d", seed, rounds))

	runtime.GC()
	runtime.GC()
	for i := 0; i < filled; i++ {
		n := ring[i]
		ck(n != nil && n.class == cls && n.val == n.id*scale+delta,
			fmt.Sprintf("ring node corrupted id=%d seed=%d", i, seed))
	}
	ck(p.id == 41 && p.class == cls && p.val == 410, "payload after GC")
	ck(len(classInits) == 1 && platformScale() == scale, "selected constants stable after GC")
	finish("l15_unix_arch_partition")
}
