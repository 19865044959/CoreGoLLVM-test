---
id: l14_method_chain_traceback
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_method_chain_traceback/main.go:29 提升方法 panicNow
  - cases/L14_debug_info/l14_method_chain_traceback/main.go:37 step1 指针接收者
  - cases/L14_debug_info/l14_method_chain_traceback/main.go:43 step3 转入嵌入方法
  - cases/L14_debug_info/l14_method_chain_traceback/main.go:46 recover 抓栈
  - docs/DIMENSIONS.md:22 L14 调试信息（栈展开）
tags: [L14, traceback, method-symbol, pointer-receiver, embedded-method, debug.Stack, gc]
requires: []
runtime: "<3s"
---

# 场景

`step1 -> step2 -> step3 -> (*methodBase).panicNow` 组成指针接收者方法链，最后经嵌入类型提升方法触发 panic；在 `runMethodChain` 的 defer 中 recover 并抓 `debug.Stack()`。模拟服务里通过方法链/组合对象崩溃时的 traceback。

## 期望行为

1. recover 到原 `*chainPayload`，payload 的 id/next 链正确；
2. 栈中包含真实方法符号 `main.(*methodWorker).step1`、`step2`、`step3` 与提升方法 `main.(*methodBase).panicNow`，以及 `main.runMethodChain`；
3. 帧顺序由内向外：panicNow < step3 < step2 < step1（只比较相对顺序）；
4. 栈带 `goroutine` 头与至少 5 处 `main.go:<行号>`（不依赖具体行号值）；
5. GC 后 payload 指针链仍有效；
6. 退出码 0，最后一行 `ALL PASS l14_method_chain_traceback`。

## 失败判读

- 方法符号缺少接收者形态（`(*methodWorker)` / `(*methodBase)`）：方法符号生成错误；
- 提升方法帧缺失：嵌入方法符号未生成或栈展开丢帧；
- 帧顺序错乱：traceback 帧遍历/内联信息错误；
- GC 后 payload 损坏：panic 帧的栈映射漏标。

## 参数

无随机路径；方法全部带 `//go:noinline`，保证符号稳定。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_method_chain_traceback/*.go
GODEBUG=clobberfree=1 /tmp/case
```
