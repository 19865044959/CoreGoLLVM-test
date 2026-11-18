---
id: l14_debug_stack_large_buf
dim: L14_debug_info
priority: P2
sources:
  - cases/L14_debug_info/l14_debug_stack_large_buf/main.go:71 64 个阻塞 goroutine 的全量 dump
  - cases/L14_debug_info/l14_debug_stack_large_buf/main.go:80 32 层递归 debug.Stack
  - docs/DIMENSIONS.md:22 L14 调试信息（栈 dump 与行号表）
tags: [L14, debug.Stack, runtime.Stack, large-buffer, truncation, goroutine-dump, TORTURE_ROUNDS, gc]
requires: []
runtime: "<10s（默认 1 轮；TORTURE_ROUNDS 放大轮数，上限 16）"
---

# 场景

64 个 goroutine 阻塞在 channel 接收上，用 2MiB 缓冲执行 `runtime.Stack(buf, true)`，验证全量 dump 不截断且每个阻塞 goroutine 都出现；随后用 32 层 noinline 递归调用 `debug.Stack()`，验证其能从初始小缓冲增长并保留所有递归帧与行号。`TORTURE_ROUNDS` 放大 dump 轮数。

## 期望行为

1. 每轮 `n < len(buf)`（未截断），`main.dumpBlocker(` 计数 >= 64；
2. `goroutine <id> [chan receive]:` 计数 >= 64，且存在 `main.go:<行号>`；
3. 放行后 64 个 goroutine 的 payload 指针在 GC 后全部有效（errs==0）；
4. 深栈 `debug.Stack()` 长度 > 1024，包含 `main.stackProbe`、`main.stackRec`，递归帧与 `main.go:<行号>` 均 >= 32；
5. 退出码 0，最后一行 `ALL PASS l14_debug_stack_large_buf`。

## 失败判读

- `n == len(buf)`：dump 被截断，缓冲区容量计算或 runtime.Stack 写入越界；
- 阻塞 goroutine 计数不足：部分 goroutine 栈展开丢失；
- 深栈帧数不足：debug.Stack 缓冲增长或行号表截断；
- 放行后指针损坏：阻塞帧栈映射漏标。

## 参数

- `TORTURE_ROUNDS`：dump 轮数，默认 1，范围 1..16（上限封顶）；
- `UAF_SEED` 不参与逻辑。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_debug_stack_large_buf/*.go
TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
