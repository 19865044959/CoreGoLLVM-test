---
id: l14_stack_growth_frames
dim: L14_debug_info
priority: P2
sources:
  - cases/L14_debug_info/l14_stack_growth_frames/main.go:80 递归叶子上抓 debug.Stack 与 CallersFrames
  - cases/L14_debug_info/l14_stack_growth_frames/main.go:101 每层 96 字节局部数组的深递归
  - cases/L14_debug_info/l14_stack_growth_frames/main.go:115 新 goroutine 入口
  - cases/L14_debug_info/l14_stack_growth_frames/main.go:120 深度扫描帧符号/文件/行号并 GC 校验
  - docs/DIMENSIONS.md:22 L14 调试信息（栈展开）
tags: [L14, stack-growth, morestack, stack-copy, debug.Stack, CallersFrames, TORTURE_ROUNDS, gc]
requires: []
runtime: "<10s（默认深度 512；TORTURE_ROUNDS 每轮 +256 层，上限 2048）"
---

# 场景

新 goroutine 的初始栈很小，递归函数每层持有 96 字节局部数组和 payload 指针，深度 512 起即可迫使运行时 morestack 分配并复制栈。在被复制的栈上抓取 `debug.Stack` 与 `runtime.CallersFrames`，必须保留深层递归帧、真实符号与 per-frame `main.go:行号`。`TORTURE_ROUNDS` 逐轮加大深度（+256，上限 2048）。

## 期望行为

1. 叶子捕获的栈文本长度足够（>5000 字节），证明深递归 traceback 完整；
2. `debug.Stack` 保留深层递归帧（≥90 层，`runtime.Stack` 的 traceback 上限内）且每帧都有 `main.go:<行号>`；
3. 栈中包含 `main.sgCapture(`、`main.sgWorker(`、`goroutine` 头；
4. `runtime.CallersFrames` 返回的 `main.sgRecurse` 帧数 ≥ depth-4；
5. 每个递归帧的 File 以 `main.go` 结尾、Line>0，且 `runtime.FuncForPC(Entry).Name()` 等于帧函数名；
6. 每轮 GC 后递归帧持有的 payload 与指针链仍有效；
7. 退出码 0，最后一行 `ALL PASS l14_stack_growth_frames`。

## 失败判读

- 递归帧数不足：栈复制后栈展开中断（与 L04/L17 交叉）；
- 行号为 0 或文件错误：栈复制后行号表 PC 偏移错误；
- Entry 名称不一致：栈复制后 PC 到符号表映射错误；
- payload 失效：递归帧/栈映射漏标指针；
- 超时：深递归栈溢出或 GC 饥饿。

## 参数

- `TORTURE_ROUNDS`：轮数，默认 1，范围 1..8；深度从 512 起每轮 +256，上限 2048；
- `UAF_SEED` 不参与逻辑。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_stack_growth_frames/*.go
TORTURE_ROUNDS=4 GODEBUG=clobberfree=1 /tmp/case
```
