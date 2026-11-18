---
id: l04_mutex_contention_loop
dim: L04_scheduler_stack
priority: P1
sources:
  - caddy@v2.10.0: admin_test.go:147 100 个 goroutine 并发 Load 配置，RWMutex 保护当前配置指针
  - tailscale@v1.80.2: derp/derp_test.go:863 RWMutex 保护 forwarder 映射的并发热更新与转发
tags: [L04, mutex, contention, critical-section, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 Caddy 配置热加载与 tailscale DERP forwarder 的锁争用：12 个 worker 加主协程各自固定 256 次抢同一把 `sync.Mutex`。每次临界区递增共享计数，每第 17 次换入一条新的 3 节点指针链并追加到 kept 表，持锁时 `Gosched` 放大排队；共享链兼作引用目标，GC 在争用进行中与结束后各执行一次。

# 期望行为

1. 总进入次数严格等于 `13 × 256`，无丢失；
2. 临界区内读到的共享链始终满足 `next.v == v+1`、`next.next.v == v+2`；
3. 任意时刻临界区并发数峰值为 1；
4. kept 表长度等于 `floor(总进入次数/17) × 3`，每条换入链的值等于 `base + 17k + {0,1,2}`；
5. 争用中与结束 GC 后，最新链与全部 kept 链仍完整；
6. 进程退出码为 0，最后一行输出 `ALL PASS l04_mutex_contention_loop`。

# 失败判读

- `total=... want=...`：临界区丢失更新或 worker 提前退出；
- `max active=...`：互斥失效或 active 计数位置错误；
- `keep=...`：换入次数与进入次数不匹配，检查第 17 次条件；
- `chain corrupted`：锁保护下的指针写入在 GC 后损坏，优先怀疑写屏障/栈映射；
- 超时：锁未释放或 Gosched 后调度饥饿。

# 参数

- `UAF_SEED`：链值种子；
- `TORTURE_ROUNDS`：轮数，默认 2（1–10）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_mutex_contention_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
