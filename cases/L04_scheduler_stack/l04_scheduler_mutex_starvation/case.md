---
id: l04_scheduler_mutex_starvation
dim: L04_scheduler_stack
priority: P2
sources:
  - go1.24.1: src/sync/mutex_test.go:199 TestMutexFairness 用持续抢锁的 goroutine 观察 Mutex 公平性
  - go1.24.1: src/sync/mutex_test.go:205 公平性测试中的紧循环 Lock/Unlock
tags: [L04, scheduler, starvation, mutex, GOMAXPROCS, preemption, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 2 轮，TORTURE_ROUNDS 可放大）"
---

# 场景

模拟单 P 下“热锁持有者持续抢锁、waiter 长时间得不到临界区”的调度饥饿观察：程序临时 `GOMAXPROCS(1)`，hog goroutine 在紧循环中反复 `Lock/Unlock` 同一把 `sync.Mutex`（每 4096 次 `Gosched` 一次，兼容关闭 async preempt 的 profile）。3 个 waiter 各自持有 3 节点栈上指针链，先登记 attempts 再等待 start；hog 先跑过 1024 次临界区后放 waiter 进入锁队列，此时显式 GC；hog 必须在固定迭代预算 4,000,000 内让全部 waiter 获得锁，否则判定饥饿失败。轮数由 `TORTURE_ROUNDS` 放大。

# 期望行为

1. hog 在 waiter 进入前已取得至少 1024 次临界区；
2. 3 个 waiter 全部在 hog 的迭代预算内获得锁，且各只服务一次；
3. hog 未达到 maxAcq 预算（达到即读取为饥饿失败）；
4. 每次临界区内 waiter 观测共享计数 > 0，锁语义未破坏；
5. waiter 阻塞期间 GC 后，仍能校验 3 节点链值；
6. 每轮结束后 waiter 链完整，最终 GC 后仍完整；
7. 最后一行输出 `ALL PASS l04_scheduler_mutex_starvation`。

# 失败判读

- `starvation: hog hit maxAcq`：waiter 在预算内始终未获得锁（调度/锁饥饿）；
- served 数不足/重复：waiter 唤醒丢失或临界区进入多次；
- `chain corrupted in critical section` / after round：GC 与锁排队期间栈指针未保留；
- hog 未取得锁：单 P 调度未推进；
- 超时：waiter 或 hog 永久阻塞。

# 参数

- `UAF_SEED`：链基值与轮次扰动（默认 1）；
- `TORTURE_ROUNDS`：轮数倍率，总轮数 = 2 × 该值（默认 1，即 2 轮）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_scheduler_mutex_starvation/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 TORTURE_ROUNDS=8 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
