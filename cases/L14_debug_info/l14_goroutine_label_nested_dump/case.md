---
id: l14_goroutine_label_nested_dump
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_goroutine_label_nested_dump/main.go:68 - glLeaf 在标签作用域内 panic
  - cases/L14_debug_info/l14_goroutine_label_nested_dump/main.go:71 - glPanicWork 的 recover 抓栈
  - cases/L14_debug_info/l14_goroutine_label_nested_dump/main.go:123 - 外层 pprof.Do 与内层 pprof.Do
  - cases/L14_debug_info/l14_goroutine_label_nested_dump/main.go:184 - profile 标签行数量断言
  - cases/L14_debug_info/l14_goroutine_label_nested_dump/main.go:187 - 嵌套标签合并精确文本
  - docs/DIMENSIONS.md:22 - L14 调试信息（goroutine dump/labels）
tags: [L14, goroutine-dump, pprof-labels, nested-labels, panic, runtime.Stack, gc]
requires: []
runtime: "<3s"
---

# 场景

goroutine A 在外层 `pprof.Do` 标签 `{outer:v1, keep:k}` 内再嵌套 `{outer:v1b, inner:i1}`；它在内层标签作用域中 panic（`glLeaf`）并 recover 抓 `debug.Stack()`，随后仍在标签作用域内阻塞。goroutine B 只带 `{keep:k2, outer:v2}` 阻塞，goroutine C 无标签阻塞。抓 `pprof.Lookup("goroutine").WriteTo(...,1)`，验证嵌套标签的覆盖/合并、标签集合隔离，以及崩溃栈与标签行互不污染。

## 期望行为

1. A 的 `debug.Stack()` 含 `main.glLeaf(`、`main.glPanicWork(`、goroutine 头与 `main.go:<行号>`，recover 到的 payload 指针一致；
2. `debug.Stack()` 中**不出现** `# labels:` 行（标签只由 pprof profile 输出）；
3. goroutine profile 中恰好有 2 个 `# labels:` 行，且包含全部 3 个阻塞在 `main.glBlock` 的 worker；
4. 嵌套标签行的精确文本为 `# labels: {"inner":"i1", "keep":"k", "outer":"v1b"}`：内层覆盖同名 `outer`，保留外层 `keep`，新增 `inner`；
5. 仅外层标签的 B 精确输出 `# labels: {"keep":"k2", "outer":"v2"}`；无标签的 C 不产生标签行；
6. 覆盖前的 `"outer":"v1"` 不出现，`"inner":"i1"` 与 `"keep":"k2"` 各只出现一次；
7. 释放后 3 个 worker 全部退出；显式 GC 后各自 payload 链有效；最后一行 `ALL PASS l14_goroutine_label_nested_dump`。

## 失败判读

- 标签行数不是 2：嵌套标签丢失/重复，或无标签 goroutine 被误标；
- 嵌套标签文本中 `outer` 仍为 `v1`：内层标签未覆盖外层；
- 缺少 `keep:k`：内层 `pprof.Do` 未继承外层标签；
- profile 缺少 `main.glBlock` 或行号：goroutine profile 调试信息缺失；
- debug.Stack 出现 `# labels:`：栈 dump 与标签元数据串扰。

## 参数

无随机路径。抓 profile 前等待 3 个 worker 就绪并轮询直到两条标签行与 3 个 `main.glBlock` 帧同时出现，避免采样到启动中的 goroutine；断言只依赖固定标签值与符号，不依赖 goroutine id。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_goroutine_label_nested_dump/*.go
GODEBUG=clobberfree=1 /tmp/case
```
