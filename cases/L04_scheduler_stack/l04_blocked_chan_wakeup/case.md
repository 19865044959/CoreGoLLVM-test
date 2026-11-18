---
id: l04_blocked_chan_wakeup
dim: L04_scheduler_stack
priority: P1
sources:
  - nats-server@v2.11.0: server/ipqueue_test.go:253 三生产者 + 通知 channel + 关闭广播
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:141 close 时排空队列并拒绝新任务
tags: [L04, channel, close-wakeup, select, sudog, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

覆盖三类 channel 唤醒路径，模拟 NATS ipqueue 关闭广播与 gRPC callback serializer 的 close 排空：

1. 16 个接收者阻塞在无缓冲 `chan *node` 上，主协程在它们全部就绪后 GC，再 `close(ch)` 唤醒；
2. 16 个发送者 `select { case out <- p; case <-done }` 阻塞（`out` 容量 1 且已填满），GC 后 `close(done)` 唤醒；
3. 带缓冲 channel 预置 2 个指针后 `close`，GC 扫描 channel 缓冲区，再由 16 个接收者排空（2 个真实值 + 14 个 nil）。

每个被唤醒的 goroutine 在阻塞前持有独立 `*node`，唤醒并跨过 gate 后再次校验。

# 期望行为

1. 接收者从 closed channel 读到 `nil, false`，本地指针内容不变；
2. 发送者只走 `<-done` 分支，不向已满 channel 投递；`out` 中原有哨兵仍在；
3. 带缓冲 closed channel 恰好产出 2 个不同指针值和 14 个 nil，无其他值；
4. GC 分别发生在接收者阻塞于 `<-ch`、发送者阻塞于 select、指针位于 channel 缓冲区时；
5. 最后一行输出 `ALL PASS l04_blocked_chan_wakeup`。

# 失败判读

- `close wake lost pointer`：close 唤醒路径上 sudog/栈指针丢失或返回了错误值；
- `done wake failed (sent=true)`：select 发送者错误地完成了发送；
- `unexpectedly delivered a pointer`：满 channel 数据被破坏；
- `buffered closed channel drain`：close 后缓冲区指针丢失/重复；
- `SIGSEGV`：唤醒后访问已回收对象。

# 参数

- `UAF_SEED`：节点初值（默认 1）；每个 phase 固定 16 个 goroutine，无时间阈值。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_blocked_chan_wakeup/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 GOGC=1 GOMAXPROCS=1 GODEBUG=clobberfree=1 /tmp/case
```
