---
id: l04_cond_bounded_buffer
dim: L04_scheduler_stack
priority: P1
sources:
  - go1.24.1: src/sync/cond_test.go:52 TestCondSignalGenerations 用谓词+代际区分 Cond 睡眠/唤醒
  - tailscale@v1.80.2: control/controlbase/conn_test.go:317 readSink 用 sync.NewCond(&Mutex)，:341 以 Cond.Wait 等待 Reader 侧进展
tags: [L04, sync.Cond, mutex, bounded-buffer, producer-consumer, blocking, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟有界任务队列的双条件变量实现：容量 6 的切片缓冲，4 个生产者共 48 项，3 个消费者按 FIFO 取走。notEmpty/notFull 共用一把 Mutex。先让 3 个消费者睡在空队列的 notEmpty 上并显式 GC；再启动生产者填满缓冲，消费者被 pause 谓词保持在 notEmpty 上继续睡眠，直到 4 个生产者全部睡在 notFull、缓冲满且全是 `*cbItem` 时再次 GC；随后解除 pause 并 Broadcast，生产/消费推进到全部完成。GC 覆盖满队列与空队列两种 cond 等待状态。

# 期望行为

1. 空队列时 3 个消费者确实进入 notEmpty 等待；满队列时 4 个生产者确实进入 notFull 等待；
2. 缓冲长度永不超过容量 6；
3. 每个生产者恰好交付 12 项，序号 0..11 不重不漏；
4. 每项的 `(prod,seq)` 计算值正确，消费者观测到的值一致；
5. 结束时缓冲为空、所有生产者/消费者退出；
6. 满队列 GC 后缓冲内指针项内容不变，最终 GC 后已消费序号集合仍完整；
7. 最后一行输出 `ALL PASS l04_cond_bounded_buffer`。

# 失败判读

- 消费者未全部进入等待：谓词/信号丢失或 pause 逻辑错误；
- 生产者未全部进入 notFull：消费者未真正暂停，容量语义被绕过；
- 序号缺失/重复：Signal 丢唤醒或在错误的谓词上睡眠；
- `buffer overflow`：notFull 循环条件错误；
- `chain corrupted` / 值错误：满队列 GC 未正确扫描缓冲/栈指针；
- 超时：Broadcast 缺失，消费者或生产者永久睡眠。

# 参数

- `UAF_SEED`：项目值公式种子（默认 1）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_cond_bounded_buffer/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=9 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
