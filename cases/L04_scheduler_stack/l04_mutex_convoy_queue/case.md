---
id: l04_mutex_convoy_queue
dim: L04_scheduler_stack
priority: P1
sources:
  - caddy@v2.10.0: admin_test.go:147 100 个 goroutine 并发 Load 配置，RWMutex 保护当前配置指针
  - tailscale@v1.80.2: derp/derp_test.go:863 一个 goroutine 循环更新 RWMutex 保护的 forwarder 映射，主 goroutine 并发转发
tags: [L04, mutex, convoy, waiter-queue, blocking, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

owner goroutine 先取得 `sync.Mutex` 并持锁等待放行；10 个 waiter 依次尝试加锁，排队期间各自在栈上持有私有指针链。主协程确认所有 waiter 已发起加锁后 GC（此时 owner 仍持锁、waiter 阻塞在锁队列上），再放行 owner；owner 解锁后 waiter 依次获得锁，读到共享链并记录自己的服务序号。

# 期望行为

1. 每个 waiter 恰好获得一次锁，服务序号构成 1..N 的排列（无丢失、无重复）；
2. waiter 在锁内看到 owner 写入的共享链（v, v+1, v+2）完好，generation 为 1；
3. 任意时刻最多一个 waiter 处于锁内（`active` 峰值为 1）；
4. 排队期间 GC 后，waiters 的私有链与共享链均完整；
5. 最后一行输出 `ALL PASS l04_mutex_convoy_queue`。

# 失败判读

- 服务序号重复/缺失：锁交接丢失唤醒或 waiter 被重复调度；
- active 峰值 >1：互斥语义破坏（锁/解锁配对错误）；
- “shared chain corrupted”：持锁期间共享指针在 GC 后失效；
- “private chain corrupted”：waiter 在阻塞队列上时栈/闭包指针未被扫描；
- 超时：owner 未释放或 waiter 未从锁队列唤醒。

# 参数

- `UAF_SEED`：链基值（默认 1）；
- `TORTURE_WAITERS`：waiter 数（默认 10，范围 2..32）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_mutex_convoy_queue/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=9 TORTURE_WAITERS=24 GOGC=1 GOMAXPROCS=2 /tmp/case
```
