---
id: l01_generic_sharded_map
dim: L01_generics
priority: P2
sources:
  - kitex@v0.13.0: pkg/remote/trans/netpollmux/shard_map_test.go:27 32 分片 map：key 0/负数不命中，1..1023 store/load/delete 且遍历和恰为 1023
  - gorm@v1.30.0: tests/lru_test.go:350 1000 goroutine 并发 Add 100 个 key，最终 Len()==100
  - gorm@v1.30.0: tests/prepared_stmt_test.go:153 100 goroutine 并发 Create/First，PrepareStmt 缓存必须去重且不死锁
tags: [L01, generics, sync.RWMutex, sharded-container, concurrent-container, pointer-identity, gc, TORTURE_ROUNDS, UAF_SEED]
requires: [generics]
runtime: "<3s（默认 8 worker × 48–64 项；TORTURE_ROUNDS≤64 约 0.5s）"
---

# 场景

实现 `sharded[K ~string|~int64, V]`：16 个 `sync.RWMutex` + `map[K]*V` 分片，
自有 FNV 风格哈希。8 个 worker 在互不重叠的 key 区间并发 Put/Update/Delete，
每 32 次操作显式 GC；结束后逐 key 复算存活项、权重与指针身份，再做并发只读
命中统计。随后以 `sharded[int64,int64]` 第二实例化验证不同 K/V 实例互不干扰。
对应 kitex 分片 map 与 gorm 并发 LRU/语句缓存。

# 期望行为

1. 并发写删后每个存活 key 的指针、id、name、weight（初始 `id*2+1` 再 +1）正确，被删 key 不可见；
2. `Len` 等于按 `i%7==3` 删除规则复算的存活数；
3. `Snapshot` 的指针和与逐个 Get 的权重和一致；
4. 并发只读阶段命中数等于存活数；
5. `int64` 键/值实例化的 Put/Update/Delete 结果按公式复算正确，与 string 实例互不影响；
6. 缺失 key 的 Update 返回 false；
7. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_generic_sharded_map`。

# 失败判读

- `sharded map lost pointer identity` / `... corrupted after GC`：分片 bucket 中的 `*V` 未被扫描或实例字段偏移错误；
- `deleted sharded entry is still visible`：Delete/分片路由错误（非 GC 问题）；
- `sharded len ...`：Len 汇总或存活规则复算不一致；
- `concurrent read hits ...`：只读锁路径返回了错误的可见性；
- `int64 entry ...`：不同 K/V 实例化共享了错误的类型参数布局；
- `FAILED ... seed=`：失败输出带 UAF_SEED。

# 参数

- `UAF_SEED`：决定每 worker 基础条数（48..64），失败输出带种子；
- `TORTURE_ROUNDS`：每 worker 条数倍数（默认 1；上限 64，string 侧约 32768 项）。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_generic_sharded_map/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=16 GODEBUG=clobberfree=1 /tmp/case
```
