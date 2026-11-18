---
id: l14_stack_growth_pc_capture
dim: L14_debug_info
priority: P2
sources:
  - cases/L14_debug_info/l14_stack_growth_pc_capture/main.go:78 - 捕获调用点 PC
  - cases/L14_debug_info/l14_stack_growth_pc_capture/main.go:93 - 同源码行双次浅栈抓取
  - cases/L14_debug_info/l14_stack_growth_pc_capture/main.go:107 - 深递归 + 每帧局部数组触发栈复制
  - cases/L14_debug_info/l14_stack_growth_pc_capture/main.go:221 - 返回并 GC 后重新展开旧 PC
  - docs/DIMENSIONS.md:22 - L14 调试信息（栈展开/复制）
tags: [L14, stack-growth, morestack, CallersFrames, lineinfo, gc]
requires: []
runtime: "<10s（默认 3 轮、深度 64/128/192；TORTURE_ROUNDS 放大轮数，上限 16，深度上限 1024）"
---

# 场景

先在浅栈上捕获调用点 PC 并展开为符号/文件/行号；再进入深层 `//go:noinline` 递归，每帧持有 192 字节局部数组，迫使运行时多次 morestack、分配并复制栈。在叶子上抓 `debug.Stack()` 与 `runtime.Callers` 帧；返回并显式 `runtime.GC()` 后，重新展开此前捕获的浅层/深层 PC，帧信息必须完全不变。模拟深调用链服务在栈增长后崩溃、日志中的旧 PC 仍需可解析的场景。

## 期望行为

1. 浅栈捕获首帧为 `main.sgShallowB`，同一源码行连续两次抓取的帧序列完全一致；
2. 深递归每层局部数组在栈复制后内容正确（corrupt=false），深栈 PC 展开出 depth+1 个 `main.sgGrow` 帧；
3. 深栈 `debug.Stack()` 在 traceback 上限内保留 90 个以上递归帧，含 `main.go:<行号>` 与 goroutine 头；
4. 所有深帧 File 以 `main.go` 结尾、Line>0，Entry 反查与帧名一致；
5. 返回并显式 GC 后，浅栈与深栈的旧 PC 展开结果（函数/文件/行号）与增长前逐项相同；
6. 每轮 payload 链与最终显式 GC 后的 payload 有效；
7. 最后一行 `ALL PASS l14_stack_growth_pc_capture`。`runtime.Stack`/`debug.Stack` 自身最多展开 100 帧，深于该上限的校验使用 `runtime.Callers`。

## 失败判读

- 局部数组内容损坏：栈复制/栈映射错误；
- 旧 PC 展开后符号或行号变化：栈复制后 PC 调整/行号表错误；
- 深栈帧数不足：morestack 丢帧或帧展开被截断；
- `debug.Stack` 出现重复帧：traceback 帧遍历错误。

## 参数

- `TORTURE_ROUNDS`：轮数，默认 3，上限 16；每轮深度为 `64 + 64*round`，上限 1024；
- 无随机路径，无 `UAF_SEED`。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_stack_growth_pc_capture/*.go
TORTURE_ROUNDS=3 GODEBUG=clobberfree=1 /tmp/case
```
