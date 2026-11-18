---
id: l19_os_pipe_write_cancel
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - caddy@v2.10.0: caddytest/integration/stream_test.go:22 io.Pipe 写 body、流式连接关闭不卡死
  - nats-server@v2.11.0: server/client_test.go:1945 写 deadline 制造 slow consumer，关闭后连接注册表归零
  - grpc-go@v1.71.1: internal/buffer/unbounded_test.go:125 Close 后写入返回错误，关闭前已入队数据仍可读出
tags: [L19, os-pipe, write-cancel, blocked-write, epipe, pointer, gc, UAF_SEED]
requires: []
runtime: "<2s"
---

# 场景

模拟流式响应写端在下游消失后的 netpoll 唤醒：写 goroutine 一次写入 4MiB，
必然因管道容量不足阻塞。读端分别验证三条路径——写满后直接关闭读端、读走
4KiB 后关闭读端、先关闭读端再写入；写端必须返回非零已写字节与错误，
不能永久挂起或被 SIGPIPE 终止。

## 期望行为

1. 阻塞写被读端关闭唤醒，返回 `err != nil`，已写字节数小于 4MiB；
2. 部分消费路径中已写字节数 ≥ 4096，且读到的 4096 字节 pattern 正确；
3. 写 goroutine 栈上的三节点指针链在 GC 后仍正确；
4. 关闭读端后读取返回 `os.ErrClosed`，再写返回错误；
5. 进程退出码 0，最后一行 `ALL PASS l19_os_pipe_write_cancel`。

## 失败判读

- 写端超时未返回：读者关闭没有唤醒阻塞写，属 netpoll/pipe 唤醒缺陷；
- `FAIL: ... n=...`：已写字节数不合理（写入未发生或越过管道容量）；
- `SIGSEGV`：payload 在 GC 中被错误回收或写缓冲越界。

## 参数

- `UAF_SEED`：指针链基址种子；
- 无其他 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_os_pipe_write_cancel/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
