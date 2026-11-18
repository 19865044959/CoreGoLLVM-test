---
id: l19_netpipe_chain
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - nats-server@v2.11.0: test/norace_test.go:295 多节点内存 transport（net.Pipe）转发，连接元数据不随节点数爆量
  - grpc-go@v1.71.1: internal/transport/transport_test.go:2821 Close 必须等 reader goroutine 完成，避免关闭后仍有读取回调
  - tailscale@v1.80.2: net/memnet/pipe_test.go:33 内存 pipe 的阻塞读/写与唤醒语义
tags: [L19, net-pipe, multi-hop, netpoll, blocking-io, pointer, gc, UAF_SEED]
requires: [net]
runtime: "<2s"
---

# 场景

模拟 NATS 多节点内存 transport 的逐跳转发：writer 经 4 段 `net.Pipe`、3 个 relay goroutine 组成的链路发送 96 个 48 字节定长帧。reader 读到首帧后阻塞在 gate 上，链路饱和，后续帧停留在各 relay 的读缓冲与栈上；主协程此时显式 `runtime.GC()` 并分配垃圾，再放行 reader。每个 relay 校验 magic/checksum 并持有自己的三节点指针链，reader 解码为指针消息后分批 GC 校验。

# 期望行为

1. 96 帧逐跳送达，3 个 relay 与 reader 的帧计数均等于 96；
2. relay/reader 解码的 checksum、id、base 全部正确，无坏帧；
3. 主协程在链路饱和时 GC 后，relay 与 reader 持有的指针链内容完整；
4. 所有 goroutine 正常退出，writer Close 后链路逐跳传播 EOF；
5. 最后一行输出 `ALL PASS l19_netpipe_chain`。

# 失败判读

- `relay N seen=...` / `reader got=...`：帧丢失或链路提前关闭；
- `bad frames=...`：帧在途被破坏（帧格式或栈上缓冲问题）；
- `payload chain corrupted`：relay/reader 栈或堆上的指针在 GC 后失效；
- `writer error` / `reader error`：管道关闭/同步协议异常；
- 超时：reader gate 或 relay 链路死锁。

# 参数

- `UAF_SEED`：base 值种子（默认 1）；
- 固定 3 跳、96 帧、48 字节定长帧；所有等待使用 5s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_netpipe_chain/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
