---
id: l19_tcp_dial_cancel
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - etcd@v3.5.0: client/pkg/transport/timeout_dialer_test.go:23 回环 TCP 上阻塞读写与 Dialer 超时语义
  - quic-go@v0.50.1: transport_test.go:122 Dial/Close 并发时错误必须可区分，不能混淆为超时
  - grpc-go@v1.71.1: test/gracefulstop_test.go:101 GracefulStop 关闭 listener 后新连接被立即拒绝
tags: [L19, tcp, dial, context-cancel, deadline, netpoll, pointer, gc, UAF_SEED]
requires: [net]
runtime: "<2s"
---

# 场景

模拟服务启动/关闭阶段的拨号取消与超时：已取消的 `context`、已过期的
`context.WithDeadline`、已过期的 `net.Dialer.Deadline` 都必须在建立连接前失败，
且错误可判别。随后用监听器上的过期 Accept deadline 证明失败拨号没有留下半开连接；
正常拨号仍可完成一次带指针 payload 的请求/应答，最后验证 listener 关闭后 `Accept`
返回 `net.ErrClosed`。

## 期望行为

1. 已取消 ctx 的 `DialContext` 返回 `context.Canceled`，无连接对象；
2. 已过期 ctx deadline 的 `DialContext` 返回 `context.DeadlineExceeded`；
3. 已过期 `Dialer.Deadline` 的 `Dial` 立即返回 timeout 类错误；
4. 过期 Accept deadline 立即返回 `os.ErrDeadlineExceeded`，说明没有残留连接；
5. 正常拨号/接受后帧内容与校验和正确，GC 后指针链不变；
6. 关闭 listener 后 `Accept` 返回 `net.ErrClosed`，退出码 0，末行 `ALL PASS l19_tcp_dial_cancel`。

## 失败判读

- `FAIL: ...`：取消/超时错误类型不对，或取消的拨号仍建立了连接；
- 第 4 步 Accept 接住连接：拨号在 ctx/deadline 检查前就进入建连，属取消路径缺陷；
- 超时：Accept 或读写阻塞未按预期唤醒，属 netpoll 唤醒缺陷。

## 参数

- `UAF_SEED`：payload 基址种子；
- 无其他 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_tcp_dial_cancel/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
