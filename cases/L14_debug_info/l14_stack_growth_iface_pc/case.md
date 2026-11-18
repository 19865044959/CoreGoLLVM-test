---
id: l14_stack_growth_iface_pc
dim: L14_debug_info
priority: P2
sources:
  - cases/L14_debug_info/l14_stack_growth_iface_pc/main.go:89 - sgCapture 在叶子抓 runtime.Callers PC
  - cases/L14_debug_info/l14_stack_growth_iface_pc/main.go:96 - 接口递归 sgImpl.hit，每层 192 字节局部数组
  - cases/L14_debug_info/l14_stack_growth_iface_pc/main.go:115 - sgImpl.call 经接口 sgGlobal.hit 继续递归
  - cases/L14_debug_info/l14_stack_growth_iface_pc/main.go:163 - 栈复制后逐帧局部数组完整
  - cases/L14_debug_info/l14_stack_growth_iface_pc/main.go:187 - 每层一个 hit/call 帧
  - docs/DIMENSIONS.md:22 - L14 调试信息（栈增长/PC 展开）
tags: [L14, stack-growth, interface-dispatch, CallersFrames, FileLine, gc]
requires: []
runtime: "<10s（默认 2 轮，深度 256/384；TORTURE_ROUNDS≤16，深度上限 1024）"
---

# 场景

递归链 `hit -> call -> sgGlobal.hit -> ...` 全部经接口动态分派，每层 `hit` 持有 192 字节局部数组，迫使运行时多次 morestack、分配并复制栈。叶子在深度 0 抓 `runtime.Callers` PC、`runtime.Caller(1)` 的接口调用点行号与 `debug.Stack()`；返回途中逐层校验局部数组。返回、栈收缩并显式 GC 后，同一批 PC 必须展开成完全相同的帧。

## 期望行为

1. 每层局部数组在栈复制后内容完整（`corrupt=false`），PC 切片覆盖全部递归层；
2. 展开后恰好有 depth 个 `main.(*sgImpl).hit` 帧与 depth 个 `main.(*sgImpl).call` 帧；首帧为 `main.(*sgImpl).call`；
3. 每个 hit 帧 File 以 `main.go` 结尾、Line>0，Entry 反查与帧名一致；
4. 每个 call 帧的 Line 等于叶子 `runtime.Caller(1)` 记录的接口调用点行号，且 `FuncForPC(f.PC).FileLine(f.PC)` 与展开帧一致；
5. `debug.Stack()` 含 `main.(*sgImpl).hit(`、`main.(*sgImpl).call(`、goroutine 头、`main.go:<行号>`，并保留至少 20 个 hit 帧（受 traceback 100 帧上限）；
6. 返回并显式 GC 后，旧 PC 展开的函数/文件/行号/Entry 与增长前逐项相同；
7. 每轮与最终 GC 后 payload 链有效；最后一行 `ALL PASS l14_stack_growth_iface_pc`。

## 失败判读

- `corrupt=true`：栈复制/栈映射错误，帧局部数组被破坏；
- hit/call 帧数少于 depth：PC 捕获丢失或接口分派帧未展开；
- call 帧行号不等于 Caller 记录值：接口调用点行号归属错误；
- GC 后帧信息变化：栈复制后 PC 调整或行号表不稳定；
- debug.Stack 缺少 hit/call：深栈 traceback 截断异常。

## 参数

- `TORTURE_ROUNDS`：轮数，默认 2，上限 16；每轮深度 `256 + 128*round`，上限 1024；
- 无随机路径，无 `UAF_SEED`。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_stack_growth_iface_pc/*.go
TORTURE_ROUNDS=2 GODEBUG=clobberfree=1 /tmp/case
```
