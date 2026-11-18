---
id: l03_build_loop_gc
dim: L03_async_preempt
priority: P2
sources:
  - nats-server@v2.11.0: server/norace_test.go:8101 5 账号 × 长循环投递，GC 每 10k 次，校验消息总量
  - nats-server@v2.11.0: server/norace_test.go:2570 200k 条 8KB 消息分批发布/消费，每批强制 GC 观察内存
tags: [L03, async-preempt, map-build, slice-build, pointer-chain, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

长循环逐轮构造 `map[int64]*node`、`[]*node` 和 `next` 指针链，插入顺序由 `UAF_SEED` 决定的排列控制；每 4 轮显式 `runtime.GC()`，同时另一 goroutine 持续分配垃圾并 GC。默认 16 轮（每轮 96 个节点），通过 `TORTURE_ROUNDS` 放大规模。最后一轮的容器与链在全部 GC 后校验。

# 期望行为

1. 构造期间至少一轮背景 GC 完成；
2. 所有轮次节点值总和等于固定公式值；
3. 最后一轮 map 与 slice 长度均为 96，按 key 查回的节点值正确；
4. 从链头遍历得到 96 个互不重复的节点；
5. 进程退出码为 0，最后一行 `ALL PASS l03_build_loop_gc`；
6. 同一 `UAF_SEED` + `TORTURE_ROUNDS` 下结果一致。

# 失败判读

- `FAIL: GC completed during build loop (seed=...)`：GC 未在长构造循环期间完成；
- `FAIL: build-loop checksum (seed=...)`：累加值或节点值被破坏；
- `FAIL: map/slice/chain intact after GC (seed=...)`：容器元素丢失、指针链断裂或 key 查回错误；
- `FAIL: chain length (seed=...)`：链被提前截断（写屏障/回收异常）；
- 失败信息携带种子，复现时使用同样的 `UAF_SEED`。

# 参数

- `UAF_SEED`：每轮插入排列种子，默认 1，失败输出带出；
- `TORTURE_ROUNDS`：构造轮数（默认 16）；放大时耗时线性增长，例如 `TORTURE_ROUNDS=256`。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_build_loop_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=5 TORTURE_ROUNDS=256 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
