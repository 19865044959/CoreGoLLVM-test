---
id: l17_tailcall_variadic_accum
dim: L17_stackmap
priority: P1
sources:
  - nats-server@v2.11.0: test/service_latency_test.go:1226（TestServiceAndStreamStackOverflow 递归解析栈溢出回归）
  - hugo@v0.155.0: hugolib/pagecollections_test.go:664（TestRegularPagesRecursive 递归遍历页面树）
tags: [L17, stackmap, tail-call, mutual-recursion, interface-dispatch, variadic, accumulator, defer-unwind, GC, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

`evenFold` / `oddFold` 与接口方法 `impl.fold` 三者互为尾位置调用，每层向 `...any` accumulator 追加 `*item`、box 或字符串；接口每层分发一次。每个递归帧注册一个 defer，在栈回卷时 GC 并重扫本层持有的 accumulator 切片（itab/data 与 backing array 都要可扫）。最内层把完整 accumulator 记录到全局，caller 在全部 defer 执行完、再次 GC 后重算 sum/tail，并与返回值对比。另校验 caller 原始 `start` 切片在调用后仍有效。

`UAF_SEED` 决定递归深度（16–40）与起始 item 值。

# 期望行为

1. 每层 defer 在回卷时 GC 后都能遍历本层 accumulator，元素类型/指针字段完好；
2. 最内层返回的 sum 与 caller 在 GC 后重扫完整 accumulator 的 sum 一致；
3. 返回的 tail 指针与重扫得到的 tail 相同；
4. caller 的 `start` 切片（item/字符串/nil）在深层 append 与 GC 后仍有效；
5. 接口分发路径与函数尾位置路径都被覆盖；
6. 退出码 0，最后一行 `ALL PASS l17_tailcall_variadic_accum`。

# 失败判读

- `even/odd-defer: ... corrupted`：栈回卷时本帧 accumulator 切片或元素指针未被扫描；
- `sum across GC` / `tail across GC`：accumulator 在 GC 后内容变化或指针丢失；
- `accumulator did not grow`：变参转发链断裂；
- `caller start slice corrupted`：caller 变参 backing array 被破坏；
- SIGSEGV：接口 data 或 item 指针失效。

# 参数

- `UAF_SEED`：决定递归深度与起始值，失败输出带该值；
- 固定 6 轮。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_tailcall_variadic_accum/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
