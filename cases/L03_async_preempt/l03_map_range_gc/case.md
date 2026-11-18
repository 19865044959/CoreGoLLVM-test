---
id: l03_map_range_gc
dim: L03_async_preempt
priority: P1
sources:
  - tailscale@v1.80.2: net/connstats/stats_test.go:82 NumCPU 个 goroutine 更新分片计数 map，周期回调聚合，GC 穿插
  - etcd@v3.5.0: server/lease/lessor_test.go:105 Detach 与 10 个 goroutine 并发遍历 lease item 集合
tags: [L03, async-preempt, map-range, pointer-chase, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

构建 `map[int]*mNode`（4096 项，插入顺序由 `UAF_SEED` 的随机排列决定），每个节点持有指向下一个节点的 `next`。主 goroutine 反复 range 该 map，统计元素数、`v` 与 `next.v`；每 8 轮在遍历之间分配垃圾并显式 `runtime.GC()`，另有一个 GC pump 并发执行 8 轮 GC。map 遍历顺序随机，但校验与顺序无关。

# 期望行为

1. 并发 GC pump 至少完成一轮；
2. 每轮 range 都恰好遍历 4096 项，单轮和等于构造时计算的参考和；
3. 累计和等于 `轮数 × 参考和`；
4. GC 后 map 长度仍为 4096，所有节点字段与 `next` 指针不变；
5. 进程退出码为 0，最后一行 `ALL PASS l03_map_range_gc`；
6. 同一 `UAF_SEED` 下判定结果一致。

# 失败判读

- `FAIL: GC completed during map range loop (seed=...)`：range 长循环期间 GC 未完成；
- `FAIL: every map range pass checksum (seed=...)`：某轮元素缺失或节点值/next 被破坏；
- `FAIL: map range aggregate checksum (seed=...)`：累计和与参考不符，可能是遍历被提前终止；
- `FAIL: map nodes intact after GC (seed=...)`：GC 后节点被回收或写屏障缺失；
- 超时：map 遍历与 GC 互锁。

# 参数

- `UAF_SEED`：map 插入排列种子，默认 1，失败输出带出；
- 遍历轮数、显式 GC 周期（8 轮）、pump 轮数均为固定常量。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_map_range_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=5 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
