---
id: l19_tcp_halfclose_bidir
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - fasthttp@v1.59.0: tcplisten/tcplisten_test.go:88 客户端写完请求后 CloseWrite，再 io.ReadAll 读取完整响应（半关闭方向独立）
  - grpc-go@v1.71.1: internal/transport/transport_test.go:2821 Close 必须等 reader goroutine 完成，关闭后不得再有读取回调
  - nats-server@v2.11.0: server/norace_test.go:1009 多类监听器并发拨号后 Shutdown，已接受连接必须收到 EOF/错误而非超时
tags: [L19, tcp, half-close, closewrite, simultaneous-fin, eof, netpoll, pointer, gc, UAF_SEED]
requires: [net]
runtime: "<2s"
---

# 场景

模拟双向同时半关闭：客户端与服务端各自写满 16 个定长帧（互不等待），随后
双方各自调用 `CloseWrite` 发 FIN（两条 FIN 可在网络中交错），再分别读取对端
数据直到 `io.EOF`。每个方向 16 帧解码为含三节点指针链的消息；写入后、读到
中点时与最终 GC 后都重新校验。最后验证本地 `CloseWrite` 之后再写必失败、EOF
粘滞。

# 期望行为

1. 服务端与客户端各收到对端 16 帧，id/name/base/checksum 与指针链全部正确；
2. 双方 `CloseWrite` 均返回 nil，随后读取对端数据后得到 `io.EOF`；
3. 再次 `Read` 仍返回 `io.EOF`（粘滞），不得返回超时或数据；
4. 本地 `CloseWrite` 之后的 `Write` 必须返回错误，不得静默成功；
5. 各 GC 检查点（写完后、读到第 8 帧时、最终）payload 指针链完整；
6. 最后一行输出 `ALL PASS l19_tcp_halfclose_bidir`，失败退出码为 1。

# 失败判读

- `server/client peer frame ... corrupted`：半关闭方向状态错误或 netpoll 读路径
  在 GC 下丢数据；
- `final read ... want EOF`：对端 FIN 未传播，或 CloseWrite 被误当成整连接关闭；
- `write after local CloseWrite succeeded`：半关闭状态机错误；
- `EOF not sticky`：关闭后的读错误语义异常；
- 超时：双方在半关闭后互相等待（deadline 仅用于防死锁）。

# 参数

- `UAF_SEED`：frame payload 基址种子（runner 每次重复注入不同值）；
- 固定每方向 16 帧、48 字节帧；10s deadline 仅防死锁，不做耗时阈值断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_tcp_halfclose_bidir/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
