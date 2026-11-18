---
id: l03_loop_defer_accum
dim: L03_async_preempt
priority: P1
sources:
  - etcd@v3.5.0: server/etcdserver/util_test.go:94 被包装 Stringer panic 时用 defer/recover 回退到替代文本
  - grpc-go@v1.71.1: internal/cache/timeoutCache_test.go:51 TTL cache 每项到期回调与 Remove/Clear 的清理语义
tags: [L03, async-preempt, defer-in-loop, defer-record, pointer-capture, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

循环每轮做两件与 defer 有关的事：调用 `//go:noinline` 的 `deferWork`，其内部 `defer` 在每次调用返回时执行并读取节点指针；同时在主循环体内登记一个带 `*dNode` 参数的 defer，这些登记项直到 `main` 返回才按 LIFO 执行。每 1024 轮分配垃圾并显式 `runtime.GC()`，GC 时需要扫描大量尚未执行的 defer 记录中的指针；另有一个 GC pump 并发执行 8 轮 GC。访问顺序由 `UAF_SEED` 派生。

# 期望行为

1. 并发 GC pump 至少完成一轮，循环内显式 GC 至少执行一次；
2. 循环共执行 8192 轮；
3. helper 的即时 defer 执行 8192 次，其和等于全部节点值之和；
4. 主循环登记的 defer 在函数返回时执行 8192 次，和等于全部节点值之和；
5. 主循环累加和等于“节点值 + 1”的逐轮和；
6. 全部 defer 执行完后节点字段不变，退出码 0，最后一行 `ALL PASS l03_loop_defer_accum`。

# 失败判读

- `FAIL: ... defer count (seed=...)`：defer 记录丢失或重复执行；
- `FAIL: ... defer sum (seed=...)`：defer 捕获的指针参数被 GC 破坏；
- `FAIL: defer helper checksum (seed=...)`：循环体计算被破坏；
- `FAIL: defer-loop nodes intact after GC (seed=...)`：节点被提前回收；
- 超时：defer 链或 GC 死锁。

# 参数

- `UAF_SEED`：决定节点访问步长与偏移，默认 1，失败输出带出；
- 循环轮数 8192、显式 GC 周期 1024、pump 8 轮均为固定常量。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_loop_defer_accum/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=9 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
