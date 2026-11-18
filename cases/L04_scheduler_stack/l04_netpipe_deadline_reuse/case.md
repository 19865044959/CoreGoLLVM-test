---
id: l04_netpipe_deadline_reuse
dim: L04_scheduler_stack
priority: P1
sources:
  - tailscale@v1.80.2: net/dns/manager_tcp_test.go:202 在 net.Pipe 连接上 SetWriteDeadline，:222 再设 SetReadDeadline
  - nats-server@v2.11.0: server/client_test.go:1420 写超时路径，:1424 用 SetWriteDeadline(time.Time{}) 清除后复用连接
tags: [L04, net.Pipe, deadline, netpoll, timeout, reuse, blocking-io, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 gRPC 伪传输与 tailscale memnet 的 deadline 复用：同一对 `net.Pipe` 连接上重复 8~11 轮，每轮：

1. 设 2ms Read deadline 且无 writer，读 goroutine 阻塞在 netpoll 超时路径上，此时主协程分配垃圾并显式 GC；
2. 期望读返回 `os.ErrDeadlineExceeded`；清除 Read deadline 后，用配对 writer 正常交换 16 字节并校验指向量；
3. 无 reader 时对 4KB Write 设 2ms deadline，期望写超时且传输 0 字节；
4. 清除 Write deadline 后由 reader 排空，重试同一块数据必须成功且内容一致。

整个过程验证超时错误、deadline 复位、netpoll 唤醒与指针链存活。

# 期望行为

1. 每轮读超时错误 `errors.Is(err, os.ErrDeadlineExceeded)` 为真；
2. 清除 deadline 后同一连接可正常读，payload id/校验和正确；
3. 每轮写超时为 `os.ErrDeadlineExceeded` 且 n==0；
4. 清除 deadline 后重试写完整发送 4096 字节，reader 收到完全相同内容；
5. 每个超时/GC 点之后，连接持有方的 2 节点指针链值不变；
6. 最终 GC 后链仍完整；
7. 最后一行输出 `ALL PASS l04_netpipe_deadline_reuse`。

# 失败判读

- 超时错误类型不符：net.Pipe deadline/netpoll 路径异常；
- 超时后正常读写失败：deadline 未被清除或管道状态被污染；
- `timed-out write transferred N>0`：部分写未按超时丢弃，复用语义被破坏；
- 重试 payload 不一致：超时/GC 后连接内部缓冲或指针损坏；
- 超时：deadline 未唤醒阻塞的 Read/Write。

# 参数

- `UAF_SEED`：轮数（8 + seed%4）、链值与填充字节种子（默认 1）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_netpipe_deadline_reuse/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=5 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
