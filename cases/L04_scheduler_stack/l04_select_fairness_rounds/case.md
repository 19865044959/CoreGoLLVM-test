---
id: l04_select_fairness_rounds
dim: L04_scheduler_stack
priority: P0
sources:
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:37 100 个 goroutine 并发调度并与单消费者串行执行，调度序不得丢失
  - nats-server@v2.11.0: server/ipqueue_test.go:253 三生产者推队列 + 单消费者 select 通知 channel，批量消费不得漏项
tags: [L04, select, fairness, channel, starvation, goroutine, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

6 个 worker，各自有两条始终就绪的 buffered channel（各预填满 token）和一条永不就绪的 channel。worker 执行固定 2048 次三路 `select`，中途停在屏障上由主协程 GC，再继续后半程。统计两个就绪分支被选中的次数与 token 集合。

# 期望行为

1. 永不就绪的 `never` 分支永不被选中；
2. 两路就绪分支都被选中且份额不低于总次数的 1/8（固定迭代下无饥饿）；
3. 每个 worker 恰好消费 2048 个 token，无 nil、无重复、值域正确；
4. 屏障处 GC 与最终 GC 后，已消费 token 的链（v, v+1）仍完整；
5. 最后一行输出 `ALL PASS l04_select_fairness_rounds`。

# 失败判读

- `never` 被选中：select 就绪判定错误（运行时/编译器缺陷）；
- 某分支计数为 0 或严重偏斜：select 分支选择伪随机化失效，造成饥饿；
- token 重复/缺失：channel 唤醒丢失或同一元素被投递两次；
- “corrupted after final GC”：被局部切片引用的 token 未被正确保活。

# 参数

- `UAF_SEED`：token 基值（默认 1）；
- `TORTURE_WORKERS`：worker 数（默认 6，范围 1..16）；
- `TORTURE_ITERS`：每个 worker 的 select 次数（默认 2048，范围 256..16384）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_select_fairness_rounds/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=7 TORTURE_WORKERS=12 TORTURE_ITERS=8192 GOGC=1 GOMAXPROCS=1 /tmp/case
```
