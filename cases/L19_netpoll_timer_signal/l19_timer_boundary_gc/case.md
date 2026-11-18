---
id: l19_timer_boundary_gc
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - nats-server@v2.11.0: server/events_test.go:3173 账号连接数事件 timer 周期上报，计数归零时 Stop，GC 后 timer 不可达
  - quic-go@v0.50.1: internal/utils/timer_test.go:40 timer Reset/到期回调闭包的生命周期
tags: [L19, afterfunc, timer-heap, liveness, unique-reference, pointer, gc, UAF_SEED]
requires: []
runtime: "<2s"
---

# 场景

验证"timer heap 是闭包及其捕获对象的唯一保活来源"这一边界：`schedule()` 内部创建一条三节点指针链并只由 `time.AfterFunc(250ms, func(){...})` 的闭包引用；函数返回后调用方没有任何引用。64 个 timer 全部在堆中排队时，主协程连续三次显式 GC 并分配额外堆垃圾，再等待全部回调。每个回调回报 id、校验和与指针内容，主协程检查触发唯一性。

# 期望行为

1. 64 个 timer 全部触发，每个 id 恰好出现一次；
2. 每个回调读到的指针链内容正确（`base/base+1/base+2`），校验和正确；
3. 多轮 GC（timer 仍在堆中）后无 payload 损坏；
4. 所有回调结束后再次 GC，无重复回调、无额外触发；
5. 最后一行输出 `ALL PASS l19_timer_boundary_gc`。

# 失败判读

- `timer N payload corrupted after GC`：timer heap 未正确保活闭包捕获的指针（扫描/写屏障问题）；
- `duplicate callback` / `extra callback`：timer 重复触发或队列摘除异常；
- `timer N never fired` / `only x/64 timers fired`：timer 丢失；
- 超时：回调未调度。

# 参数

- `UAF_SEED`：base 值种子（默认 1）；
- 固定 64 个 timer、250ms 到期；等待使用 10s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_timer_boundary_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
