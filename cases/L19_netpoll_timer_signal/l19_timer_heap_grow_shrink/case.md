---
id: l19_timer_heap_grow_shrink
dim: L19_netpoll_timer_signal
priority: P2
sources:
  - nats-server@v2.11.0: server/events_test.go:3173 连接数事件 timer 周期上报，计数归零时 Stop，停止后不可再回调
  - quic-go@v0.50.1: internal/utils/timer_test.go:40 10 次 Reset 交替读取 channel 与过窗，timer 生命周期跨 GC
  - etcd@v3.5.0: server/lease/lease_queue_test.go:22 批量租约 timer 的插入、到期与取消
tags: [L19, timer-heap, afterfunc, stop, growth, shrink, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<8s（默认 4096 长 timer + 512 短 timer；TORTURE_ROUNDS 放大轮数）"
---

# 场景

模拟服务端周期性 timer 随负载批量挂入、连接/租约归零后批量 Stop 的真实路径：
第一波挂入 4096 个长周期 `time.AfterFunc` 让 runtime timer 堆增长；全部 Stop 并
丢弃引用后显式 GC，让堆收缩；再挂入 512 个短周期 timer 验证收缩后的 timer 堆
仍能正常到期回调。闭包各自捕获一条 3 节点指针链，用于检查 timer heap 与回调
goroutine 栈上的指针位图。

## 期望行为

1. 长周期 timer 在堆中时 GC，闭包捕获的指针链完好；
2. 4096 次 `Stop()` 全部返回 true，取消后长 timer 回调执行 0 次；
3. 收缩后显式 GC 不崩溃，保留观测的 16 条指针链内容不变；
4. 512 个短周期 timer 全部到期，`fired` 计数、校验和与指针链均正确；
5. 被取消的长 timer 在短 timer 全部到期后仍未触发；
6. 最后一行输出 `ALL PASS l19_timer_heap_grow_shrink`，失败退出码为 1。

## 失败判读

- `FAIL: canceled long timers fired ...`：Stop 未把 timer 从堆中摘除或回调被误唤醒；
- `FAIL: short fired=...` / 超时：timer 堆收缩后无法重新增长，或 GC 误回收桶结构；
- payload 链校验失败 / `SIGSEGV`：timer 闭包或回调栈上的指针未被正确保活；
- 超时 15s：timer 回调被卡死或 GC 饥饿。

## 参数

- `UAF_SEED`：随机路径种子，参与每条 payload 链的基值计算；
- `TORTURE_ROUNDS`：轮数（1–10），每轮重新经历“增长-收缩-再增长”。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_timer_heap_grow_shrink/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
