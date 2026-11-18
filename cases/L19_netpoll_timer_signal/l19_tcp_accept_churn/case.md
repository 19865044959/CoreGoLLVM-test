---
id: l19_tcp_accept_churn
dim: L19_netpoll_timer_signal
priority: P2
sources:
  - nats-server@v2.11.0: server/norace_test.go:1009 五类监听器并发拨号后 Shutdown，Accept 路径不得卡死
  - grpc-go@v1.71.1: test/gracefulstop_test.go:101 GracefulStop 关闭 listener 时阻塞中的 Accept 必须被唤醒
  - etcd@v3.5.0: client/pkg/transport/timeout_dialer_test.go:23 回环 TCP 上的阻塞读写与超时语义
tags: [L19, tcp, accept, deadline, listener-close, churn, netpoll, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: [net]
runtime: "<5s（默认 2 轮；TORTURE_ROUNDS 放大轮数）"
---

# 场景

模拟监听器生命周期 churn：每轮先设置已过期 Accept deadline 验证 timeout，再清空
deadline 接受 4 个并发客户端（每连接 8 个带指针帧的回显）。全部处理完后 accept
循环阻塞在第 5 次 `Accept` 上，主协程关闭 listener，要求阻塞中的 Accept 被 netpoll
唤醒并返回 `net.ErrClosed`。每轮穿插显式 GC。

## 期望行为

1. 过期 Accept deadline 立即返回 `os.ErrDeadlineExceeded`；
2. 4 个客户端 × 8 帧全部回显，服务端/客户端解码的指针链在 GC 后正确；
3. 关闭 listener 后阻塞中的 `Accept` 返回 `net.ErrClosed`；
4. 每轮无 goroutine 卡死，错误通道为空；
5. `TORTURE_ROUNDS=N` 时重复 N 轮；
6. 退出码 0，末行 `ALL PASS l19_tcp_accept_churn`。

## 失败判读

- 过期 deadline 未返回 timeout：监听器 deadline 未进入 netpoll 轮询；
- 关闭后 Accept 超时：close 未唤醒阻塞的 accept，属 netpoll 唤醒缺陷；
- `client/server ... corrupted`：帧 payload 在 GC 后损坏；
- 客户端/处理协程超时：回显链路死锁。

## 参数

- `UAF_SEED`：每轮 payload 基址种子；
- `TORTURE_ROUNDS`：轮数，默认 2，上限 50。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_tcp_accept_churn/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=5 GODEBUG=clobberfree=1 /tmp/case
```
