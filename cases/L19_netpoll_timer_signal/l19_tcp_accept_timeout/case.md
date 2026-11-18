---
id: l19_tcp_accept_timeout
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - nats-server@v2.11.0: server/norace_test.go:1009 五类监听器并发拨号后 Shutdown，Accept 路径不得卡死
  - grpc-go@v1.71.1: test/gracefulstop_test.go:101 GracefulStop 关闭 listener 时阻塞中的 Accept 必须被唤醒
  - etcd@v3.5.0: client/pkg/transport/timeout_dialer_test.go:23 回环 TCP 上的阻塞读写与超时语义
tags: [L19, tcp, accept, deadline, timeout, listener, netpoll, pointer, gc, UAF_SEED]
requires: [net]
runtime: "<2s"
---

# 场景

模拟监听器在 accept deadline 上的状态切换：固定 8 轮，先给 `*net.TCPListener`
设置已过期 deadline，`Accept` 必须立即返回 timeout；清空 deadline 后受控拨号
必须被接住并完成一次带指针 payload 的请求/应答。最后关闭 listener，验证阻塞中
的 `Accept` 返回 `net.ErrClosed` 而不是 timeout。

## 期望行为

1. 过期 deadline 下 `Accept` 返回 nil 连接与非 nil 的 `net.Error`，`Timeout()==true`；
2. 清空 deadline 后每次拨号都能在 accept 轮次内接住；
3. 服务器写出的帧在客户端 GC 后 payload 链完好，ACK 能回传；
4. `Close()` listener 后阻塞 `Accept` 返回的错误满足 `errors.Is(err, net.ErrClosed)`；
5. 8 轮结束后无非预期错误累积；
6. 最后一行输出 `ALL PASS l19_tcp_accept_timeout`，失败退出码为 1。

## 失败判读

- `Accept with expired deadline returned nil error`：listener deadline 未生效；
- `accept after clear` 超时：deadline 清空后 listener 未恢复监听；
- `Accept after Close err=... want net.ErrClosed`：关闭唤醒路径被误报为 timeout；
- payload 校验失败：netpoll 读路径在 GC 下丢数据；
- 超时 10s：Accept 或 Dial 永久阻塞。

## 参数

- `UAF_SEED`：随机路径种子，参与帧基值计算。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_tcp_accept_timeout/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
