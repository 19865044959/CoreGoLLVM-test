---
id: l04_timer_heap_pointers
dim: L04_scheduler_stack
priority: P2
sources:
  - nats-server@v2.11.0: server/thw/thw_test.go:22 时间轮 timer 增删改与到期批量回收
  - quic-go@v0.50.1: internal/utils/timer_test.go:100 同一 deadline 下 timer 读/未读/Reset 状态机
tags: [L04, timer, timer-heap, closure, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认轮数，TORTURE_ROUNDS 可放大）"
---

# 场景

模拟 NATS 时间轮与 quic-go timer 的批量到期：每轮创建 128 个 timer，其中 3/4 为 1ms 的 `time.AfterFunc`，闭包捕获一条 3 节点指针链（base、base+1、base+2）；其余 1/4 为 1 小时的 timer 并立即 `Stop`。到期回调先阻塞在 gate 上，主协程此时分配垃圾并显式 GC 两次——闭包与捕获链分别位于 timer heap、闭包对象和回调 goroutine 栈上。放行后逐个校验链内容并汇总；Stop 掉的 timer 不得执行。

# 期望行为

1. 96 个活跃 timer 全部触发，回调中指针链内容完整，无损坏；
2. 汇总校验和等于独立计算的 `Σ timerChainSum(i)`；
3. 32 个被 Stop 的 timer 回调一次都不执行（`stopped == 0`），`Stop()` 均返回 true；
4. 至少一轮（`TORTURE_ROUNDS` 默认 2）通过，每轮结束后再次 GC 并稳定；
5. 最后一行输出 `ALL PASS l04_timer_heap_pointers`。

# 失败判读

- `fired=... want=...`：timer 丢失/未触发或重复触发；
- `closure/target corrupted`：闭包捕获指针在 GC 后被破坏（timer heap 扫描/写屏障问题）；
- `stopped timer callback ran`：Stop 语义或 timer heap 摘除异常；
- 汇总和错误：指针链节点内容或数量丢失。

# 参数

- `UAF_SEED`：链节点值与每轮增量（默认 1）；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–50。每轮 128 个 timer，固定 1ms/1h 两种延时，不设置耗时阈值。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_timer_heap_pointers/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=10 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
