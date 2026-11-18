---
id: l19_os_pipe_poll
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - caddy@v2.10.0: caddytest/integration/stream_test.go:22 io.Pipe 写 body、flush 后读响应，流式连接关闭不卡死
  - tailscale@v1.80.2: net/memnet/pipe_test.go:33 内存 pipe 的阻塞读与 deadline 唤醒语义
tags: [L19, os-pipe, blocking-io, netpoll, pointer, gc, eof, UAF_SEED]
requires: []
runtime: "<1s"
---

# 场景

模拟 caddy 流式请求与 tailscale 内存 pipe 的阻塞读写：读 goroutine 先阻塞在空 `os.Pipe` 的 `Read` 上（Linux 下走文件/netpoll 轮询路径），主协程在此时显式 GC，然后写端 goroutine 写入 64 个含指针 payload 的定长帧。读端逐个解码成带三节点指针链的消息并持有；每 16 帧在持有指针对象时再次 GC。写端关闭后验证额外一次 `Read` 返回 EOF。

# 期望行为

1. 64 帧全部读取并解码，`len(msgs) == 64`；
2. 最终 GC 后每条消息 `id/base/name/指针链` 均正确；
3. 写端 Close 后读端得到 `io.EOF` 而不是挂起；
4. 读写 goroutine 无错误；
5. 最后一行输出 `ALL PASS l19_os_pipe_poll`。

# 失败判读

- `read/decode/write` 错误：管道数据不完整或帧损坏；
- `message ... corrupted after GC`：读端持有对象的指针被误回收；
- `read after writer Close`：EOF 语义异常；
- 超时：阻塞读写未被唤醒。

# 参数

- `UAF_SEED`：base 值种子（默认 1）；
- 固定 64 帧、48 字节定长帧；等待使用 5s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_os_pipe_poll/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
