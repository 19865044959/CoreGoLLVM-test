---
id: l19_tcp_dial_cancel_many
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - etcd@v3.5.0: client/pkg/transport/timeout_dialer_test.go:23 TestReadWriteTimeoutDialer 回环 TCP 拨号与读写超时语义
  - quic-go@v0.50.1: transport_test.go:122 Transport 与 Listener 并发 Close，阻塞路径错误必须可区分
  - grpc-go@v1.71.1: test/gracefulstop_test.go:101 GracefulStop 关闭 listener 后新连接必须立即失败
tags: [L19, tcp, dial-cancel, context-cancel, deadline, accept-queue, netpoll, pointer, gc, UAF_SEED]
requires: [net]
runtime: "<2s"
---

# 场景

模拟服务启动/关闭 churn 中大批被取消的拨号：对同一存活监听器执行 256 次
已取消 ctx 的顺序拨号、64 次已过期 deadline 拨号与 64 个并发取消拨号。全部
必须无连接对象返回，错误分别为 `context.Canceled` / `context.DeadlineExceeded`。
随后监听器过期 Accept deadline 立即 timeout；清空 deadline 后发起一次真实
拨号并完成带指针链的帧交换——若前面的取消拨号泄漏成已排队连接，accept 会
先取到旧连接从而在帧校验处失败。

# 期望行为

1. 256 次顺序取消拨号全部返回 `context.Canceled`，`conn == nil`；
2. 64 次过期 deadline 拨号全部返回 `context.DeadlineExceeded`；
3. 64 个并发取消拨号全部无连接且错误可判别，无 goroutine 泄漏；
4. 已过期 Accept deadline 立即返回 timeout；
5. 清空 deadline 后真实拨号/接受成功，帧与指针链在 GC 后正确，ACK 完整；
6. 最后一行输出 `ALL PASS l19_tcp_dial_cancel_many`，失败退出码为 1。

# 失败判读

- `seq/concurrent canceled dial ... returned a conn`：取消检查在建连之后才生效；
- 错误不是 `context.Canceled/DeadlineExceeded`：上下文取消未映射到拨号错误；
- `healthy accept` 读到坏帧或超时：取消拨号在监听器 accept 队列留下半开连接；
- `payload corrupted`：真实连接路径解码对象在 GC 后损坏；
- 超时：accept 队列被旧连接占用，协调链路死锁。

# 参数

- `UAF_SEED`：真实连接帧 payload 基址种子；
- 固定 256 顺序 + 64 过期 + 64 并发取消拨号；10s deadline 仅防死锁。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_tcp_dial_cancel_many/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=1 GODEBUG=clobberfree=1 /tmp/case
```
