---
id: l02_sync_once_pool_init
dim: L02_concurrency
priority: P1
sources:
  - tailscale@v1.80.2: types/lazy/sync_test.go:127 10000 goroutine 并发触发单次初始化
  - caddy@v2.10.0: modules/caddyhttp/metrics_test.go:30 sync.Once 初始化 + in-flight gauge 归零
  - fiber@v2.52.6: app_test.go:1955 ctx 池 Acquire/Release 复用并清零
tags: [L02, sync.Once, sync.Pool, concurrent-init, reuse, reset, gc]
requires: []
runtime: "<2s"
---

# 场景

模拟 tailscale lazy 单次初始化、caddy metrics 的 Once 初始化与 fiber ctx 池复用：32 个 goroutine 并发调用同一个 `sync.Once` 初始化 64 项只读表与 `sync.Pool`，同时 16 个 pool worker 也通过 `once.Do` 获取初始化结果后各做 96 轮 `Get→使用→GC→Put`。worker 在 Get 时断言对象已被上一轮 Put 清零（`used=false`、`p=nil`、buffer 长度正确），每 32 轮显式 GC 并把池化对象持有的 `*blob` 与共享表项跨 GC 重校验。

# 期望行为

1. `sync.Once` 初始化恰好执行 1 次（`once_inits == 1`）；
2. 所有初始化 goroutine 都看到完整的 64 项只读表与非空池；
3. 池返回的对象永远不含上一轮的 `*blob`/used 标志，跨 GC 后本轮 payload 与 buffer 内容不变；
4. 共享只读表项在最终 GC 后仍完整；
5. 最后一行 `ALL PASS l02_sync_once_pool_init`。

# 失败判读

- `sync.Once init ran more than once`：Once 并发语义被破坏；
- `pool returned dirty object`：Put 未清理或 Pool 把同一对象并发返回给多个 goroutine（UAF 风险极高）；
- `pooled payload corrupted by GC`：池化对象持有的堆指针被误回收；
- `shared table entry corrupted`：只读表在 GC 后损坏。

# 参数

无随机路径；`UAF_SEED` 由 runner 注入但本用例不使用。无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_sync_once_pool_init/*.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
