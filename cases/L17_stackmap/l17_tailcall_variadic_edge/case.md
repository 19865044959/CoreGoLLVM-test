---
id: l17_tailcall_variadic_edge
dim: L17_stackmap
priority: P0
sources:
  - hugo@v0.155.0: hugolib/pagecollections_test.go:731（TestRegularPagesRecursiveHome 递归遍历页面层级）
  - nats-server@v2.11.0: server/sublist_test.go:897（TestSublistRaceOnInsert 递归 subject trie 匹配，匹配循环中插入 GC）
tags: [L17, stackmap, tail-call, mutual-recursion, interface-dispatch, variadic, boundary-depth, nil-spread, GC, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

`step` 与接口方法 `impl.fold` 在尾位置互相调用，每层向 `...any` accumulator append 一个指针或字符串；接口每隔一层分发一次。递归深度仅 0..5，输入覆盖 nil 展开、非 nil 空切片、1 个元素、5 个元素与含 typed nil 的切片。每个递归帧注册 defer，在栈回卷时执行两轮 GC 并重扫本层 accumulator；caller 在返回后再 GC，重算 sum、长度、tail，并校验输入切片未被 append 改写。

`UAF_SEED` 决定每轮 base 与起始值。

# 期望行为

1. 深度 n 返回的 accumulator 长度为 `len(input)+n`，各层 append 未丢失；
2. caller 在 GC 后重扫返回 accumulator 的 sum 等于返回 sum，tail 指针有效；
3. 每层 defer 在回卷 GC 后看到的本层 accumulator 长度与内容不变；
4. 深度 0 + nil 展开返回 nil；非 nil 空切片展开保持非 nil（nil/空边界）；
5. caller 输入切片长度、sum 与 tail 在调用后不变（append 别名不影响既有元素）；
6. 接口 fold 的 receiver 在 defer GC 后仍有效；
7. 退出码 0，最后一行 `ALL PASS l17_tailcall_variadic_edge`。

# 失败判读

- `step/fold-defer: frame acc`：栈回卷时本帧变参切片或长度槽未被扫描；
- `returned sum` / `acc lost contents`：accumulator 在 GC 后内容变化；
- `acc len`：尾位置 append 链断裂或接口分发丢失；
- `caller input changed`：append 复用了 caller backing array 并破坏已有元素；
- `nil-spread depth0`：nil 与空切片语义异常；
- SIGSEGV：接口 data 或 item 指针失效。

# 参数

- `UAF_SEED`：每轮 base 种子，失败输出带该值；
- 固定 10 轮，深度 0..5，输入形态按轮次在 nil/空/1/5 元素间轮换。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_tailcall_variadic_edge/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
