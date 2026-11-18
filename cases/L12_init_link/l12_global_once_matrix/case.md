---
id: l12_global_once_matrix
dim: L12_init_link
priority: P2
sources:
  - caddy@v2.10.0: modules/caddyhttp/metrics_test.go:33 Metrics.init 为 sync.Once，in-flight gauge 归零
  - kitex@v0.13.0: internal/configutil/once_test.go:25 OnceOrPanic 二次调用 panic
  - grpc-go@v1.71.1: internal/grpcsync/event_test.go:65 Fire 只成功一次、Done 只关闭一次
tags: [L12, sync.Once, lazy-init, panic, concurrency, gc, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 8 slot × 256 迭代 × 32 goroutine；TORTURE_ROUNDS 放大 slot 与迭代）"
---

# 场景

多个独立的包级 `sync.Once`：`onceA`/`onceB` 分别构建 map 与 slice，另有一张按 slot 动态创建的 `[]sync.Once` 表。32 个 goroutine 并发首次调用，主 goroutine 同时分配垃圾并触发 GC。另测 `panicOnce`：body panic 后 Once 仍视为完成，后续 `Do` 不再执行 body。

# 期望行为

1. `onceA`/`onceB` 的构建计数恰为 1，所有 worker 观察到同一批 map 条目与 slice 元素；
2. 每个 slot 的构建计数恰为 1，值等于 `slot*7+3`；
3. 首次 slot 为 8×rounds，迭代 256×rounds（上限 8192）；`TORTURE_ROUNDS` 线性放大；
4. 首次 `tryRisky()` 返回 true 且 panic 计数 +1；后续两次均返回 false，panic 计数仍为 1；
5. 并发构建期间 GC 不导致表重建或 slot 重复构建；
6. GC 后 `ensureA/ensureB/ensureSlot` 返回相同内容，构建计数不变，panic 状态保持完成；
7. 最后一行 `ALL PASS l12_global_once_matrix`。

# 失败判读

- 构建计数 >1：`sync.Once` 语义或内存序错误；
- 不同 worker 看到不同表：全局指针写入可见性/竞态问题；
- slot 构建次数 >1：`sync.Once` 数组元素被错误复制或重置；
- 第二次 `tryRisky` 仍 panic：panic 后 Once 完成语义错误；
- 死锁/超时：Once 内重入或锁顺序问题；
- 失败行 `seed=` 可用于复现 worker 的 slot 起点。

# 参数

- `TORTURE_ROUNDS`（默认 1，上限 64）：slot 数 = `8×rounds`，迭代数 = `256×rounds`（上限 8192）；
- `UAF_SEED`：决定每个 worker 的 slot 起点并影响 GC 时机下的调度扰动。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_global_once_matrix/*.go
UAF_SEED=1 GOGC=1 /tmp/case
TORTURE_ROUNDS=8 UAF_SEED=5 /tmp/case
```
