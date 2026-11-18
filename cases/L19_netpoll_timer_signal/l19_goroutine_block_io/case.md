---
id: l19_goroutine_block_io
dim: L19_netpoll_timer_signal
priority: P2
sources:
  - grpc-go@v1.71.1: internal/transport/transport_test.go:2821 Close 必须等 reader goroutine 完成，避免 Close 后仍有读取回调
  - tailscale@v1.80.2: net/memnet/pipe_test.go:33 内存 pipe 的阻塞读/写与唤醒语义
  - caddy@v2.10.0: caddytest/integration/stream_test.go:22 流式 I/O 未结束时的 Shutdown 不卡死
tags: [L19, blocking-io, net-pipe, os-pipe, goroutine, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: [net]
runtime: "<2s（默认 1 轮 8 对管道；TORTURE_ROUNDS 放大轮数）"
---

# 场景

模拟 gRPC 传输层 reader 与 caddy 流式 I/O 的"goroutine 阻塞在 I/O 上时发生 GC"：每轮创建 8 对管道（4 对 `net.Pipe`、4 对 `os.Pipe`），每对启动一个 reader goroutine，先报告就位再阻塞在 `Read` 上。主协程确认 8 个 reader 都就位后显式 GC，再并发写入含三节点指针链的消息帧唤醒它们。每个 reader 解码后把指针对象交给主协程，主协程再次 GC 并校验，然后放行 reader 做最终校验。

# 期望行为

1. 8 对管道的数据全部到达，每对恰好一帧，无丢失/重复；
2. 主协程与 reader goroutine 在 GC 后读到的指针 payload 均正确；
3. 所有 reader/writer 正常退出，无 I/O 错误；
4. `TORTURE_ROUNDS` 放大轮数后每轮均通过；
5. 最后一行输出 `ALL PASS l19_goroutine_block_io`。

# 失败判读

- `received x/8 frames`：唤醒失败或数据丢失；
- `payload corrupted after GC`：阻塞 goroutine 栈上的指针被误回收；
- `reader/writer ...` 错误：管道同步异常；
- `readers did not finish`：Close/唤醒路径死锁。

# 参数

- `UAF_SEED`：每轮 base 值种子（默认 1）；
- `TORTURE_ROUNDS`：轮数，默认 1，范围 1–20；每轮固定 8 对管道。等待使用 10s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_goroutine_block_io/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=10 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
