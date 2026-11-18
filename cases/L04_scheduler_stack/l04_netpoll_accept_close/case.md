---
id: l04_netpoll_accept_close
dim: L04_scheduler_stack
priority: P1
sources:
  - frp@v0.67.0: pkg/util/vhost/https_test.go:12 回环监听 + accept goroutine + 关闭 listener 后 accept 循环退出
  - nats-server@v2.11.0: server/client_test.go:1945 客户端写 deadline 触发慢消费者路径，结束后连接计数归零
tags: [L04, netpoll, tcp, accept, deadline, blocking-io, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

在 `127.0.0.1:0` 建 TCP listener，accept 循环把连接交给主协程。10 个会话中 6 个 reader 阻塞在 `Read`（netpoll 等待），4 个用已过期 `SetReadDeadline` 走 netpoll 超时唤醒。normal reader 全部就绪后 GC，再写入 8 字节 payload 唤醒 reader；最后关闭 listener，验证阻塞中的 `Accept` 立即返回 `net.ErrClosed`。

# 期望行为

1. 6 个 normal 会话各读到正确 payload，回写 ack 与期望一致；
2. 4 个 deadline 会话的读错误被识别为超时（`net.Error.Timeout()` 或 `os.ErrDeadlineExceeded`），而非永久阻塞或 EOF；
3. reader 阻塞在 netpoll 上时 GC，唤醒后各自捕获的指针链仍完整；
4. close listener 后 accept goroutine 退出且错误为 `net.ErrClosed`；
5. 所有保留链在最终 GC 后完整；
6. 最后一行输出 `ALL PASS l04_netpoll_accept_close`。

# 失败判读

- deadline 会话读到数据：过期 deadline 未生效，netpoll 超时唤醒路径异常；
- accept 未在 Close 后返回：listener 关闭未唤醒 netpoll，或 accept 循环状态机错误；
- reader 读到 0 字节/EOF：连接被提前关闭或 netpoll 唤醒后状态丢失；
- “kept[...] chain corrupted”：netpoll 阻塞/唤醒期间栈或闭包指针未被正确保留。

# 参数

- `UAF_SEED`：会话链基值（默认 1）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_netpoll_accept_close/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=7 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
