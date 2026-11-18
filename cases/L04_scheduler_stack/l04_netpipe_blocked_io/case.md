---
id: l04_netpipe_blocked_io
dim: L04_scheduler_stack
priority: P1
sources:
  - grpc-go@v1.71.1: internal/transport/transport_test.go:2821 Close 必须等 reader goroutine 完成，避免关闭后仍有读取回调
  - tailscale@v1.80.2: net/memnet/pipe_test.go:83 容量 1 管道写满后阻塞，读者消费后写完成
tags: [L04, net.Pipe, blocking-io, netpoll, deadline, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 gRPC transport 用 `net.Pipe` 伪造传输层与 tailscale memnet 管道的阻塞读写：8 个会话各有一对同步 writer/reader。writer 先写 24 字节头部（阻塞到 reader 消费），然后停在 release 协调点；reader 随后阻塞在 8 字节尾部的 `ReadFull` 上。主协程收齐 8 个半程信号后显式 GC，此时 8 个 reader 阻塞于 pipe 的 netpoll 等待、8 个 writer 阻塞于协调点且各自链仍在其栈上；放行后 writer 校验链并写尾部。另用已过期 deadline 验证 `net.Pipe` 的超时唤醒路径。

# 期望行为

1. 8 个会话的头部/尾部字段（id、base、sum）全部匹配；
2. reader 读到尾部校验和等于独立公式 `3b+3`；
3. writer 在阻塞点 GC 后，栈上链仍为 `b, b+1, b+2`；
4. 会话结束后再次 GC，`keep` 中保存的 8 条链仍完整；
5. 过期 deadline 的 Read 以 `os.ErrDeadlineExceeded` 立即返回；
6. 进程退出码为 0，最后一行输出 `ALL PASS l04_netpipe_blocked_io`。

# 失败判读

- `pipe payload corrupted`：阻塞期间 pipe 缓冲区或栈上指针损坏；
- 卡在 Read/Write：netpoll 唤醒缺失或关闭顺序错误；
- `deadline did not wake`：net.Pipe 的 deadline 路径异常；
- `chain ... corrupted`：writer 栈上的链在 GC 后损坏，优先怀疑栈映射。

# 参数

- `UAF_SEED`：链值种子；
- 固定 8 会话；无 TORTURE 放大参数。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_netpipe_blocked_io/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
