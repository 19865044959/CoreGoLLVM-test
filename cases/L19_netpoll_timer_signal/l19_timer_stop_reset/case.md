---
id: l19_timer_stop_reset
dim: L19_netpoll_timer_signal
priority: P0
sources:
  - quic-go@v0.50.1: internal/utils/timer_test.go:100 同一 deadline 下 Stop/Reset 与 channel 读/未读状态机
  - quic-go@v0.50.1: internal/utils/timer_test.go:40 10 次 Reset 交替 sleep/读 channel，最后一次仍须触发
tags: [L19, timer, stop, reset, pointer, gc, deterministic]
requires: []
runtime: "<1s"
---

# 场景

模拟 quic-go 连接 timer 包装器的 Stop/Reset 语义，并用显式 GC 穿插验证 timer 对象与闭包捕获指针的存活：

1. 新建 1 小时 timer，`Stop()` 返回 true 且不投递，`Reset(1ms)` 后正常触发一次；已触发且已读空的 timer 再 `Stop()` 返回 false；
2. 停止状态的 timer 连续 8 次 `Reset(1ms)`，每次读空 channel 后重新武装，必须恰好触发 8 次；
3. 未停止的活跃 timer 直接 `Reset(1ms)`，只触发一次、不重复投递；
4. `AfterFunc` 先 `Stop()`（回调不执行），再 `Reset(1ms)` 两次，每次重新武装后回调执行，闭包捕获的指针链在 GC 后仍正确。

# 期望行为

1. 上述四组 Stop/Reset 判定全部成立；
2. Stop 后没有额外的 timer 值可读，Reset 后每次恰好一个值；
3. 指针 payload 在每次 Stop/Reset 与 `runtime.GC()` 后内容不变；
4. 最后一行输出 `ALL PASS l19_timer_stop_reset`。

# 失败判读

- `stopped timer delivered a value`：Stop 语义错误（timer 已被摘除仍触发）；
- `timer did not fire`：Reset 未重新武装或 timer 被提前回收；
- `duplicate delivery`：Reset 造成重复投递；
- `payload corrupted after GC`：timer/闭包持有的指针被误回收。

# 参数

- `UAF_SEED`：由 runner 注入但本用例不使用（无随机路径）；
- 所有等待使用 5s 上限，只用于防死锁；触发顺序由 timer channel 本身保证，无耗时阈值断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_timer_stop_reset/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
