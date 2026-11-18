---
id: l04_cond_broadcast_waves
dim: L04_scheduler_stack
priority: P1
sources:
  - tailscale@v1.80.2: net/memnet/pipe_test.go:33 内存 pipe 用 sync.Cond + 计时器做 Block/广播唤醒
  - grpc-go@v1.71.1: internal/buffer/unbounded_test.go:90 多 writer 与单 reader 的队列交接，条件变量/channel 唤醒不得丢失元素
tags: [L04, sync.Cond, broadcast, generation, sleep-wake, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

8 个 waiter 在 `sync.Cond` 上按“波次代号”入睡。每波主协程持锁把共享 payload 换成新链并 `Broadcast`，全部 waiter 必须从 `Wait` 返回、记录本波 payload 后重新入睡。每次广播前（waiter 全部 asleep、上一波 payload 仅被 waiter 局部切片引用）显式 GC。默认 5 波。

# 期望行为

1. 每个 waiter 恰好观察到 1..W 波各一次，顺序递增，无跳波/重复；
2. 每次唤醒读到的 payload 代次与波次一致，链（v, v+1）完整；
3. 每个 waiter 的 `Wait` 次数等于波数（Broadcast 不丢唤醒、不额外伪唤醒）；
4. 所有历史波次的 payload 在最终 GC 后仍完整（replaced 链不得被误回收）；
5. 最后一行输出 `ALL PASS l04_cond_broadcast_waves`。

# 失败判读

- waiter 记录波次错位：Broadcast 后谓词未重新检查，或唤醒丢失；
- wake count 大于波数：条件变量使用不当（未在循环中检查谓词）；
- “recorded chain corrupted after wave GC”：旧 payload 只被栈/局部切片引用，GC 后失效；
- 超时：某波 Broadcast 发生时仍有 waiter 未入睡，导致该波等待缺失。

# 参数

- `UAF_SEED`：payload 基值（默认 1）；
- `TORTURE_WAITERS`：waiter 数（默认 8，范围 2..24）；
- `TORTURE_ROUNDS`：波数（默认 5，范围 1..16）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_cond_broadcast_waves/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=4 TORTURE_ROUNDS=12 TORTURE_WAITERS=16 GOGC=1 GOMAXPROCS=2 /tmp/case
```
