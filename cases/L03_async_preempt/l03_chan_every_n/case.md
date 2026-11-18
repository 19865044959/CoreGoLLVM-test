---
id: l03_chan_every_n
dim: L03_async_preempt
priority: P1
sources:
  - nats-server@v2.11.0: server/norace_test.go:11455 有界队列 + 非阻塞/轮询发送 + 运行时开关切换
  - grpc-go@v1.71.1: internal/buffer/unbounded_test.go:90 10 写 1 读无界队列，校验全部元素完整读出
tags: [L03, async-preempt, channel-pingpong, every-n, message-pointer, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

主 goroutine 跑 16384 轮持针循环，每 64 轮构造一条携带 `*cNode` 的消息经 `req` 发给 worker，并从 `rep` 读回应答；worker 每处理 8 条消息就分配垃圾并显式 `runtime.GC()`，应答值依赖消息中的节点与 `next` 指针。主循环跨 channel 同步点后继续使用同一节点指针，另有 GC pump 并发执行 8 轮 GC。节点访问顺序由 `UAF_SEED` 派生。

# 期望行为

1. 并发 GC pump 至少完成一轮；
2. 请求/应答恰好 256 次，worker 处理条数与之相等；
3. 每条应答值等于 `p.v + seq + p.next.v`；
4. 主循环累加和等于逐轮节点值参考和；
5. GC 后全部节点与 `next` 指针不变；
6. 进程退出码为 0，最后一行 `ALL PASS l03_chan_every_n`。

# 失败判读

- `FAIL: chan request/reply payload (seed=...)`：消息指针被回收或应答计算被破坏；
- `FAIL: chan exchange count` / `worker processed all messages`：收发次数不符或 worker 提前退出；
- `FAIL: chan-loop pointer checksum (seed=...)`：跨 channel 同步后指针读取异常；
- `FAIL: chan-loop nodes intact after GC (seed=...)`：节点被 GC 回收；
- 超时：channel 配对死锁。

# 参数

- `UAF_SEED`：节点访问步长与偏移，默认 1，失败输出带出；
- 轮数 16384、channel 周期 64、worker GC 周期 8 均为固定常量。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_chan_every_n/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
