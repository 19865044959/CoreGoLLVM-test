---
id: l04_mutex_blocked_gc
dim: L04_scheduler_stack
priority: P1
sources:
  - caddy@v2.10.0: modules/caddyhttp/replacer_test.go:242 mapMutex 使用 RWMutex 保护占位符映射
  - caddy@v2.10.0: admin_test.go:147 100 个 goroutine 并发 Load 配置，RWMutex 保护的当前配置指针反复替换
  - tailscale@v1.80.2: derp/derp_test.go:863 一个 goroutine 循环 Add/Remove forwarder，RWMutex 保护映射，主 goroutine 并发发 5000 个包
tags: [L04, mutex, rwmutex, blocking, sema, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 Caddy 配置热加载与 tailscale DERP forwarder 的锁竞争：主协程先持有 `sync.Mutex`，默认 16 个 contender 各自构建 3 节点私有指针链、发出 ready 后阻塞在 `Lock()`；固定次数让出调度后，主协程分配垃圾并显式 GC 两次，再解锁。每个 contender 获得锁后校验链、在临界区主动 `Gosched`、解锁后再校验一次。随后用 `sync.RWMutex` 重演读者排队：主协程持写锁，8 个读者阻塞在 `RLock()`，GC 后解锁，读者校验共享只读链；最后再 GC 一次确认共享链仍存活。

# 期望行为

1. 所有 contender 恰好返回一次结果，id 无缺失/重复；
2. 每个 contender 在锁内与解锁后看到的私有链校验和均等于 `3*base+3`；
3. 总校验和等于所有 contender 期望值之和；
4. 持写锁期间 GC 后，所有 RWMutex 读者都能读到完整的共享链；
5. 写锁释放后再 GC，共享链校验和仍等于 `3*sharedBase+6`；
6. 最后一行输出 `ALL PASS l04_mutex_blocked_gc`。

# 失败判读

- `chain corrupted around mutex/GC`：阻塞在 Mutex.Lock 的 goroutine 栈上指针未被正确扫描；
- `mutex total sum`：临界区丢失、重复执行或链被破坏；
- `reader sum`：RWMutex 读者在写锁阻塞期间发生指针丢失；
- `shared chain after GC`：共享对象被提前回收（UAF）；
- 超时：主协程未解锁或 contender 在 ready 前提前返回。

# 参数

- `UAF_SEED`：链基值与共享链基值种子（默认 1）；
- `TORTURE_WORKERS`：Mutex contender 数，默认 16，范围 2–64；
- `TORTURE_READERS`：RWMutex 读者数，默认 8，范围 2–64。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_mutex_blocked_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_WORKERS=32 TORTURE_READERS=16 GOGC=1 GOMAXPROCS=2 /tmp/case
```
