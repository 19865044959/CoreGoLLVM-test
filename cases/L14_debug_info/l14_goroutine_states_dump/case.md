---
id: l14_goroutine_states_dump
dim: L14_debug_info
priority: P2
sources:
  - cases/L14_debug_info/l14_goroutine_states_dump/main.go:88 chan send 阻塞
  - cases/L14_debug_info/l14_goroutine_states_dump/main.go:97 select 阻塞
  - cases/L14_debug_info/l14_goroutine_states_dump/main.go:108 sync.WaitGroup.Wait 阻塞
  - cases/L14_debug_info/l14_goroutine_states_dump/main.go:116 sync.Cond.Wait 阻塞
  - docs/DIMENSIONS.md:22 L14 调试信息（goroutine 栈 dump）
tags: [L14, runtime.Stack, goroutine-state, chan-send, select, waitgroup, cond, TORTURE_ROUNDS, gc]
requires: []
runtime: "<10s（默认 1 轮；TORTURE_ROUNDS 放大轮数，上限 8）"
---

# 场景

每轮同时放置四个不同阻塞状态的 goroutine：chan send、select、sync.WaitGroup.Wait、sync.Cond.Wait，用 `runtime.Stack(buf, true)` 全量 dump，轮询直到四种状态头都出现；随后逐一唤醒并校验阻塞期间持有的 payload 指针。`TORTURE_ROUNDS` 放大轮数。

## 期望行为

1. dump 中出现 `goroutine <id> [chan send]:`、`[select]:`、`[sync.WaitGroup.Wait]:`、`[sync.Cond.Wait]:`；
2. dump 包含真实符号 `main.stateChanSend`、`main.stateSelect`、`main.stateWGWait`、`main.stateCondWait`、`main.main` 与 `main.go:<行号>`；
3. 四种状态全部就绪后才放行；放行后各 goroutine 看到自己的 id/name（errs==0）；
4. 每轮结束后 GC，阻塞期间持有的 payload 指针仍有效；
5. 退出码 0，最后一行 `ALL PASS l14_goroutine_states_dump`。

## 失败判读

- 某状态头缺失：goroutine 状态机/栈展开输出错误（3s 轮询上限后仍缺失即失败）；
- 符号缺失：traceback 符号表问题；
- 放行后指针错误：阻塞帧栈映射漏标；
- 超时/死锁：唤醒路径与状态不匹配。

## 参数

- `TORTURE_ROUNDS`：轮数，默认 1，范围 1..8；
- `UAF_SEED` 不参与逻辑。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_goroutine_states_dump/*.go
TORTURE_ROUNDS=4 GODEBUG=clobberfree=1 /tmp/case
```
