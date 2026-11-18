---
id: l19_tcp_loopback
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - nats-server@v2.11.0: server/norace_test.go:1009 五类监听器并发拨号后 Shutdown，已接受连接必须收到 EOF/错误而非超时
  - etcd@v3.5.0: client/pkg/transport/timeout_dialer_test.go:23 回环 TCP 上的阻塞读写与超时语义
tags: [L19, tcp, loopback, netpoll, echo, pointer, gc, UAF_SEED]
requires: [net]
runtime: "<2s"
---

# 场景

模拟 NATS 监听器并发连接与 etcd 回环传输：在 `127.0.0.1:0` 上监听，4 个客户端并发拨号，每个客户端发送 16 个含三节点指针链的 48 字节帧；服务器接受 4 个连接，每个连接由独立 goroutine 解码为指针消息、校验、必要时间隔 GC、再重新编码回显。客户端读回显后再次解码并持有一批消息，每 4 帧 GC，循环结束后统一校验。全部完成后关闭 listener 与连接。

# 期望行为

1. 4 个客户端 × 16 条消息全部回显，`echoed == 64`；
2. 服务器端解码的指针消息在 GC 后内容正确；
3. 客户端持有的回显消息在最终 GC 后内容正确，校验和与独立计算值一致；
4. 所有 goroutine 在连接关闭后正常退出，无错误；
5. 最后一行输出 `ALL PASS l19_tcp_loopback`。

# 失败判读

- `echoed=... want=64`：消息丢失或回显循环提前退出；
- `server/client ... corrupted`：网络路径解码对象在 GC 后损坏；
- `dial/accept/read/write` 错误：监听/拨号或帧协议异常；
- 超时：连接未建立或 goroutine 死锁。

# 参数

- `UAF_SEED`：base 值种子（默认 1）；
- 固定 4 客户端 × 16 消息；等待使用 10s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_tcp_loopback/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
