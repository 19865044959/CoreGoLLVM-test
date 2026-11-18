---
id: l19_udp_deadline_cycles
dim: L19_netpoll_timer_signal
priority: P2
sources:
  - quic-go@v0.50.1: transport_test.go:122 ListenUDP 回环 + 并发 Close，错误必须可区分
  - quic-go@v0.50.1: integrationtests/self/timeout_test.go:234 idle timeout 到期后连接按时关闭
  - etcd@v3.5.0: client/pkg/transport/timeout_dialer_test.go:23 回环 socket 上的阻塞读写与超时语义
tags: [L19, udp, read-deadline, write-deadline, deadline-preserves-queue, close-wakeup, netpoll, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: [net]
runtime: "<5s（默认 2 轮 × 8 个周期；TORTURE_ROUNDS 放大轮数）"
---

# 场景

模拟 UDP 收发循环反复设置/清空 deadline 的边界：每个周期先在 socket 上排入
一个数据报，然后设置已过期读 deadline——`ReadFromUDP` 必须立即 timeout 且
不得消费已排队报文；清空 deadline 后该报文必须完整读出。随后设置已过期写
deadline，写入必须失败且不发出；清空后发送 marker，服务器读到的下一个报文
必须是 marker（证明失败写没有入队）。每轮末尾让 goroutine 阻塞在
`ReadFromUDP` 上，由 `Close` 唤醒为 `net.ErrClosed`。周期中显式 GC 校验
已持有报文上的指针链。

# 期望行为

1. 过期读 deadline 下 `ReadFromUDP` 返回 `os.ErrDeadlineExceeded`，`n == 0`；
2. 清空 deadline 后排队的首个数据报仍可读，内容与指针链完整；
3. 过期写 deadline 的 `Write` 返回 `os.ErrDeadlineExceeded`，报文未发出；
4. 清空写 deadline 后的 marker 是下一个被读到的报文，id/pattern 正确；
5. 阻塞 `ReadFromUDP` 在 socket 关闭后返回 `net.ErrClosed`；
6. 默认 2 轮通过；`TORTURE_ROUNDS` 放大后每轮均通过，末行
   `ALL PASS l19_udp_deadline_cycles`。

# 失败判读

- 过期 deadline 未立即返回：UDP fd 未走 netpoll deadline 检查；
- `queued datagram ...`：过期读消费了队列中的报文或 deadline 清空未恢复；
- `marker mismatch`：过期写实际发出，或 UDP 队列残留脏报文；
- `blocked ReadFromUDP ... want net.ErrClosed`：close 未唤醒阻塞读；
- `payload chain corrupted`：已持有报文对象在 GC 后损坏。

# 参数

- `UAF_SEED`：每轮 payload 基址种子；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–50；每轮固定 8 个 deadline 周期；
- 读写等待使用 10s 防死锁 deadline，不做耗时阈值断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_udp_deadline_cycles/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 TORTURE_ROUNDS=10 GOGC=1 GOMAXPROCS=1 GODEBUG=clobberfree=1 /tmp/case
```
