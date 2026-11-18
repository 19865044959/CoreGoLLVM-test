---
id: l14_gc_panic_stack
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_gc_panic_stack/main.go:59 panic 叶子帧
  - cases/L14_debug_info/l14_gc_panic_stack/main.go:65 GC 后 recover 抓栈
  - docs/DIMENSIONS.md:22 L14 调试信息（栈展开与行号表）
tags: [L14, gc, panic-stack, traceback, clobberfree, line-info]
requires: []
runtime: "<3s"
---

# 场景

先做多轮垃圾分配并显式 `runtime.GC()`，随后立刻沿 `gcPanicOuter→gcPanicMiddle→gcPanicLeaf` 触发 panic，recover 后检查栈符号与 `main.go:<行号>`；再 GC 一次验证 panic payload 里的双向指针链未被 clobber。

## 期望行为

1. recover 到值类型 `gcBoom`，消息为 `after-gc`，payload 节点 `v=1`、next 节点 `v=2`；
2. 栈包含 `main.gcPanicLeaf`、`main.gcPanicMiddle`、`main.gcPanicOuter` 与 `goroutine` 头；
3. 栈中 `main.go:<行号>` 至少 3 处（每帧一行，不依赖具体行号值）；
4. 再执行 gcStress 后节点字段与名字仍正确；
5. 退出码 0，最后一行 `ALL PASS l14_gc_panic_stack`。

## 失败判读

- GC 后行号/符号缺失：写屏障或调试信息在 GC 周期后损坏；
- payload 字段错乱：panic 帧的栈映射漏标，对象被回收或 clobber；
- 行号正则不匹配：行号表未生成。

## 参数

固定规模；无随机路径。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_gc_panic_stack/*.go
GODEBUG=clobberfree=1 /tmp/case
GODEBUG=efence=1 /tmp/case
```
