---
id: l19_io_context_cancel
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - grpc-go@v1.71.1: test/context_canceled_test.go:37 客户端 deadline/取消与服务器返回竞争，最终状态与附属数据必须一致
  - quic-go@v0.50.1: http3/transport_test.go:365 请求 ctx 取消后连接状态与后续请求行为
  - nats-server@v2.11.0: server/client_test.go:1945 客户端阻塞写触发 slow consumer，关闭后连接注册表归零
tags: [L19, context, cancel, afterfunc, net-pipe, blocking-io, netpoll, pointer, gc, UAF_SEED]
requires: [net]
runtime: "<2s"
---

# 场景

模拟 RPC/流读取阻塞期间上游取消 context 的路径：reader 阻塞在 `net.Pipe` 的
`Read` 上，`context.AfterFunc` 在 ctx 取消时关闭连接以唤醒 Read。校验取消原因、
回调执行次数与 payload。对照路径在取消前调用 stop 撤销 AfterFunc，连接不被
关闭，带指针链的帧仍可完整传输。

## 期望行为

1. 取消后阻塞 `Read` 在 10s 保护内返回错误，错误属于 `io.ErrClosedPipe` 或
   `net.ErrClosed`；
2. `ctx.Err()==context.Canceled`，AfterFunc 回调恰好执行 1 次，回调后 `stop()`
   返回 false；
3. 被取消 goroutine 栈上的 payload 链在 GC 后完好；
4. 对照路径 stop 返回 true、回调 0 次，取消 context 不影响已 stop 的连接 IO；
5. 对照路径帧内容与指针链校验通过；
6. 最后一行输出 `ALL PASS l19_io_context_cancel`，失败退出码为 1。

## 失败判读

- `blocked Read was not unblocked`：context 取消未驱动连接关闭或 netpoll 丢唤醒；
- `Read completed with nil error`：取消后 IO 被静默视为成功；
- `AfterFunc ran N times`：取消回调重复执行；
- 对照路径失败：`stop` 未真正撤销 AfterFunc，误关闭连接；
- payload 损坏：阻塞 goroutine 栈扫描错误。

## 参数

- `UAF_SEED`：随机路径种子，参与帧基值计算。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_io_context_cancel/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
