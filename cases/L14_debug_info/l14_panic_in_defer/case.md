---
id: l14_panic_in_defer
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_panic_in_defer/main.go:66 defer 内再次 panic
  - cases/L14_debug_info/l14_panic_in_defer/main.go:85 仅 defer panic 的恢复路径
  - cases/L14_debug_info/l14_panic_in_defer/main.go:93 两个场景的栈与 payload 校验
  - docs/DIMENSIONS.md:22 L14 调试信息（栈展开）
tags: [L14, panic-in-defer, panic-replacement, recover, debug.Stack, gc]
requires: []
runtime: "<3s"
---

# 场景

场景一：函数先 panic(p1)，后注册的 defer 先运行，recover(p1) 后再次 panic(p2)，先注册的 defer 最终 recover 到 p2。场景二：无原始 panic，仅 defer 内 panic。验证 panic 值替换语义与 traceback 中的 defer 闭包符号。

## 期望行为

1. 场景一 defer 成功 recover 到 p1（firstOK），最终 recover 到 p2 且与 p1 不同；
2. 场景一栈包含 `main.panicInDeferRun.func1`（恢复闭包）、`main.panicInDeferRun.func2`（panic 闭包）、`main.panicInDeferRun`、`goroutine` 头与至少 2 处 `main.go:<行号>`；
3. 场景二 recover 到 p1，栈包含 `main.deferOnlyPanic.func2`、`main.deferOnlyPanic` 与 `main.go:<行号>`；
4. GC 后 p1/p2 双向指针链仍有效；
5. 退出码 0，最后一行 `ALL PASS l14_panic_in_defer`。

## 失败判读

- 最终 recover 仍拿到 p1：defer 内 panic 未替换原 panic 值；
- 栈中缺少 func2 符号：panic 中的 defer 帧被提前丢弃；
- 进程崩溃/退出码 2：recover 未生效，panic 逃逸到 runtime；
- GC 后指针错乱：defer 帧栈映射漏标。

## 参数

无随机路径。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_panic_in_defer/*.go
GODEBUG=clobberfree=1 /tmp/case
```
