---
id: l01_generic_sync_container
dim: L01_generics
priority: P2
sources:
  - kitex@v0.13.0: pkg/remote/trans/ttstream/container/queue_test.go:26 NewQueue[int] 生产者/消费者并发搬运数据并校验 Size/总量
  - gorm@v1.30.0: tests/generics_test.go:772 5 goroutine 复用同一条泛型查询链，各 goroutine 结果不得互相污染
  - quic-go@v0.50.1: internal/utils/ringbuffer/ringbuffer_test.go:9 泛型缓冲容器在 Push/Pop 后的指针存活
tags: [L01, generics, sync.RWMutex, concurrent-container, pointer-values, gc, TORTURE_ROUNDS]
requires: [generics]
runtime: "<3s（默认 256 条；TORTURE_ROUNDS 放大每 worker 条数，上限 64）"
---

# 场景

自建泛型 `ConcurrentMap[K,V]`（`sync.RWMutex` + `map[K]*V`），4 个 worker 在互不重叠的 key 区间并发 `Set/Update/Delete`，每 16 次插入显式 GC；结束后按区间复算存活项、权重、总和，单次快照校验指针，再让 4 个 reader 并发只读统计命中数；最后用 `ConcurrentMap[string,item]` 二次实例化复用同一批指针。对应 kitex 泛型队列与 gorm 泛型链并发复用场景。

# 期望行为

1. 并发写删后每个存活 key 的指针、id、name、weight（初始 `id*2+1` 加 1）全部正确，被删 key 不可见；
2. `Len` 等于复算存活数（每 8 个删 1 个）；
3. `Snapshot` 的指针/权重和与逐个 Get 一致；
4. 并发只读阶段命中数等于存活数；
5. `string` 键实例化复用同一批 `*item`，指针身份与载荷不变；
6. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_generic_sync_container`。

# 失败判读

- `entry ... corrupted after GC`：map bucket 中的 `*V` 未被扫描或实例化字段偏移错误；
- `deleted entry is still visible`：泛型 Delete/锁实现错误（非 GC 问题）；
- `snapshot ...`：RLock 保护下仍读到损坏指针；
- `string-key container lost pointer identity`：不同 K/V 实例化共享了错误的类型参数布局；
- `FAILED ... seed=`：失败输出带 UAF_SEED，便于复现。

# 参数

- `UAF_SEED`：种子，当前不参与数据生成，仅随失败输出；
- `TORTURE_ROUNDS`：每 worker 插入条数的倍数（默认 1 → 64 条/worker；上限 64 → 16384 条），用于长压测。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_generic_sync_container/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=16 GODEBUG=clobberfree=1 /tmp/case
```
