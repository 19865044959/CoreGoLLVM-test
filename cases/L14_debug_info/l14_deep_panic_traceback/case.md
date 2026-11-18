---
id: l14_deep_panic_traceback
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_deep_panic_traceback/main.go:62 48 层 noinline 递归链
  - cases/L14_debug_info/l14_deep_panic_traceback/main.go:79 子 goroutine panic/recover 抓栈
  - docs/DIMENSIONS.md:22 L14 调试信息（栈展开）
tags: [L14, traceback, deep-recursion, goroutine, debug.Stack, gc]
requires: []
runtime: "<3s"
---

# 场景

子 goroutine 中走完 48 层 `//go:noinline` 递归后 panic 一个携带指针链的 payload，在 defer 中 recover 并用 `debug.Stack()` 抓栈。模拟深调用链服务崩溃时 traceback 需要完整展开每一层帧的真实场景。

## 期望行为

1. recover 到 `*deepBoom`，payload 指针与节点链有效；
2. 栈中包含真实符号 `main.deepLeaf`、`main.deepRecurse`、`main.deepChainEntry`、`main.deepChainRun`；
3. `main.deepRecurse(` 帧数 >= 40，且 `main.go:<行号>` 出现 >= 40 次（只校验文件与正行号，不依赖具体行号值）；
4. 再次 GC 后 payload 的 48→47 指针链仍有效；
5. 进程退出码 0，最后一行 `ALL PASS l14_deep_panic_traceback`。

## 失败判读

- 深层递归帧缺失或计数不足：栈展开/行号表在深层栈上出错；
- `main.go:` 行号缺失：traceback 调试信息未生成；
- `FAIL: payload ... after GC`：panic 帧的栈映射漏标指针；
- SIGSEGV / bad pointer：栈复制或栈映射错误（与 L17 交叉）。

## 参数

固定 48 层递归；无随机路径，`UAF_SEED` 不影响结果。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_deep_panic_traceback/*.go
GODEBUG=clobberfree=1 /tmp/case
```
