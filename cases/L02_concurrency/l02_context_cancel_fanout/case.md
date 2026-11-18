---
id: l02_context_cancel_fanout
dim: L02_concurrency
priority: P1
sources:
  - quic-go@v0.50.1: streams_map_outgoing_test.go:456 并发打开流 + context 取消竞态
  - grpc-go@v1.71.1: test/context_canceled_test.go:126 取消与压缩标志的 select 竞争
tags: [L02, context, WithCancel, fanout, broadcast, shutdown, gc]
requires: []
runtime: "<3s"
---

# 场景

模拟 quic-go 流表与 grpc-go 取消路径的广播语义：`context.WithCancel` 后启动 8 个 worker，每个 worker 在紧凑循环里 `select ctx.Done() / default` 并推进 atomic 工作量；主 goroutine 等全部 worker 启动且共享工作量达到阈值后调用 `cancel()`，随后连续做 6 轮显式 GC + 垃圾分配。每个 worker 在观察到 Done 后校验自己启动时持有的 `*workerPayload`，再显式 GC 并重校验，最后置退出标志。若取消广播失效，worker 有固定迭代上限兜底，不会挂死。

# 期望行为

1. 8 个 worker 都在取消后退出，`exited == 8`；
2. `ctx.Err() == context.Canceled`；
3. worker 的 payload 在取消后、以及跨 GC 后内容不变；
4. 取消期间主 goroutine 的多次 GC 与 worker 退出并发执行，无 panic/数据破坏；
5. 最后一行 `ALL PASS l02_context_cancel_fanout`。

# 失败判读

- `workers exited != 8`：Done 广播未送达部分 goroutine；
- `worker hit iteration cap without observing cancel`：取消完全未生效（严重）；此时用例仍能退出并报错；
- `worker payload ... corrupted`：worker 栈/寄存器中的指针被误回收；
- 超时：worker 或主 goroutine 卡死。

# 参数

无随机路径；`UAF_SEED` 由 runner 注入但本用例不使用。无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_context_cancel_fanout/*.go
GODEBUG=clobberfree=1 GOMAXPROCS=2 /tmp/case
```
