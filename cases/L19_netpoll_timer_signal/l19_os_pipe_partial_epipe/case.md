---
id: l19_os_pipe_partial_epipe
dim: L19_netpoll_timer_signal
priority: P2
sources:
  - caddy@v2.10.0: caddytest/integration/stream_test.go:22 流式响应写 body，连接关闭时写入方不得卡死
  - grpc-go@v1.71.1: internal/buffer/unbounded_test.go:125 Close 后写入返回错误，关闭前已入队数据仍可读出
  - nats-server@v2.11.0: server/client_test.go:1945 写 deadline 制造 slow consumer，关闭后连接注册表归零
tags: [L19, os-pipe, blocked-write, epipe, partial-write, reader-close, netpoll, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 2 轮 × 8 次部分写；TORTURE_ROUNDS 放大轮数）"
---

# 场景

模拟流式响应写端在下游消失时的部分写边界：每次创建一个 `os.Pipe`，写者
单次 `Write` 4MiB（必然因容量不足阻塞）；读者只读取固定 12KiB 前缀并校验
pattern，随后关闭读端。阻塞写必须返回“已写部分字节 + 错误”，而不是永久
挂起或整段失败；之后再写继续失败。写者阻塞期间主协程显式 GC，扫描其阻塞
栈上的指针链。每轮重复 8 次，`TORTURE_ROUNDS` 放大轮数。

# 期望行为

1. 读者读到的 12KiB 前缀与确定性 pattern 逐字节一致；
2. 读端关闭后阻塞写返回 `err != nil`，已写字节数 `>= 12KiB` 且 `< 4MiB`；
3. 错误满足 `syscall.EPIPE` 或 `os.ErrClosed`，进程不得被 SIGPIPE 终止；
4. 同一写端再次 `Write` 返回 `n == 0` 与非 nil 错误；
5. 写者闭包捕获的三节点指针链在阻塞期间与 GC 后完整；
6. 默认 2 轮通过；`TORTURE_ROUNDS` 放大后每轮均通过，末行
   `ALL PASS l19_os_pipe_partial_epipe`。

# 失败判读

- 写端超时未返回：读端关闭未唤醒阻塞写，属 netpoll/pipe 唤醒缺陷；
- `partial n=...`：已写字节数不合理（未写入前缀或越过容量写出全部 payload）；
- `write err=... want EPIPE/ErrClosed`：关闭后的写错误类型异常；
- 进程被 SIGPIPE 终止：写路径信号处理缺陷；
- `payload chain corrupted`：写者阻塞栈上的指针在 GC 后失效。

# 参数

- `UAF_SEED`：pattern 与 payload 基址种子；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–20；每轮固定 8 次部分写；
- 写端返回等待使用 10s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_os_pipe_partial_epipe/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 TORTURE_ROUNDS=10 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
