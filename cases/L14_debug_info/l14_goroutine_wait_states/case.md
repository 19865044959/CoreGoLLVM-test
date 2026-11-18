---
id: l14_goroutine_wait_states
dim: L14_debug_info
priority: P2
sources:
  - cases/L14_debug_info/l14_goroutine_wait_states/main.go:91 sync.RWMutex.RLock 阻塞
  - cases/L14_debug_info/l14_goroutine_wait_states/main.go:100 sync.RWMutex.Lock 阻塞
  - cases/L14_debug_info/l14_goroutine_wait_states/main.go:109 nil channel send 阻塞
  - cases/L14_debug_info/l14_goroutine_wait_states/main.go:116 nil channel receive 阻塞
  - cases/L14_debug_info/l14_goroutine_wait_states/main.go:123 无 case 的 select 阻塞
  - cases/L14_debug_info/l14_goroutine_wait_states/main.go:128 每轮放置五种状态并全量 dump
  - docs/DIMENSIONS.md:22 L14 调试信息（goroutine 栈 dump）
tags: [L14, runtime.Stack, goroutine-state, rwmutex, nil-channel, select-no-cases, TORTURE_ROUNDS, gc]
requires: []
runtime: "<10s（默认 1 轮；TORTURE_ROUNDS 放大轮数，上限 8）"
---

# 场景

每轮同时放置五种阻塞状态的 goroutine：`sync.RWMutex.RLock`、`sync.RWMutex.Lock`、nil channel send、nil channel receive、无 case 的 `select{}`，用 `runtime.Stack(buf, true)` 全量 dump 并轮询直到五种状态头都出现。这些状态覆盖调试时最常见的“读锁竞争 / 写锁竞争 / nil channel 误用 / 空 select 挂起”现场。`TORTURE_ROUNDS` 放大轮数。

## 期望行为

1. dump 中出现 `goroutine <id> [sync.RWMutex.RLock]:`、`[sync.RWMutex.Lock]:`、`[chan send (nil chan)]:`、`[chan receive (nil chan)]:`、`[select (no cases)]:`；
2. dump 包含真实符号 `main.waitRLock`、`main.waitWLock`、`main.waitNilSend`、`main.waitNilRecv`、`main.waitNoCase`、`main.main` 与 `main.go:<行号>`；
3. 五种状态全部就绪后才校验；RLock/Lock 阻塞者被放行后看到自己的 payload（errs==0）；
4. 每轮 GC 后，阻塞/放行 goroutine 持有的 payload 指针仍有效；
5. 退出码 0，最后一行 `ALL PASS l14_goroutine_wait_states`。

## 失败判读

- 某状态头缺失：goroutine 状态机/栈展开输出错误（超过 3s 轮询上限即失败）；
- 符号缺失：traceback 符号表问题；
- RLock/Lock 放行后 errs 非 0：阻塞帧栈映射漏标；
- 超时/死锁：唤醒顺序与锁状态不匹配。

## 参数

- `TORTURE_ROUNDS`：轮数，默认 1，范围 1..8；
- nil channel 与空 select goroutine 每轮保持阻塞到进程结束，属于预期泄漏；
- `UAF_SEED` 不参与逻辑。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_goroutine_wait_states/*.go
TORTURE_ROUNDS=4 GODEBUG=clobberfree=1 /tmp/case
```
