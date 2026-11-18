---
id: l19_tcp_accept_multi_close
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - grpc-go@v1.71.1: test/bufconn/bufconn_test.go:195 TestCloseWhileAccepting 阻塞 Accept 在 listener Close 后返回 errClosed
  - grpc-go@v1.71.1: test/gracefulstop_test.go:101 GracefulStop 关闭 listener 时阻塞中的 Accept 必须被唤醒
  - nats-server@v2.11.0: server/norace_test.go:1009 多类监听器并发拨号后 Shutdown，不得留下打开的连接
tags: [L19, tcp, accept, multi-accept, listener-close, errclosed, dial-after-close, netpoll, pointer, gc, UAF_SEED]
requires: [net]
runtime: "<2s"
---

# 场景

模拟监听器关闭时的多 Accept 唤醒边界：同一 `net.TCPListener` 上 8 个 goroutine
同时挂在 `Accept` 上，关闭 listener 后 8 个调用全部必须返回 `net.ErrClosed`
且无连接对象。之后对已关闭地址的 8 次拨号必须失败。再新建监听器验证关闭
风暴后 netpoll 仍健康：一次正常拨号/接受完成带指针链的请求-应答，GC 后校验
解码对象。

# 期望行为

1. 8 个阻塞 Accept 全部被 Close 唤醒，错误满足 `errors.Is(net.ErrClosed)`；
2. 任何 Accept 都不得返回非 nil 连接；
3. Listener 关闭后的 8 次拨号全部失败且不产生连接；
4. 新监听器的拨号/接受成功，帧内容与三节点指针链在 GC 后正确，ACK 完整；
5. 所有 goroutine 在 10s 防死锁上限内退出；
6. 末行 `ALL PASS l19_tcp_accept_multi_close`，失败退出码为 1。

# 失败判读

- `accept ... not woken by listener close`：close 未广播唤醒全部 Accept；
- `accept err=... want net.ErrClosed`：关闭错误未映射或返回 timeout；
- `accept ... returned a conn after close`：关闭后仍接受连接；
- `dial ... after close succeeded`：监听器关闭语义错误；
- `payload corrupted`：健康路径解码对象在 GC 后损坏。

# 参数

- `UAF_SEED`：健康路径帧 payload 基址种子；
- 固定 8 个并发 Accept、8 次关闭后拨号；10s 上限仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_tcp_accept_multi_close/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
