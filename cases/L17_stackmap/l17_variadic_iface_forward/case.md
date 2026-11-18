---
id: l17_variadic_iface_forward
dim: L17_stackmap
priority: P0
sources:
  - badger@v4.5.0: logger_test.go:30（mockLogger.Errorf(format, v ...interface{}) 内部 v... 转发给 fmt.Sprintf）
  - gorm@v1.30.0: tests/tracer_test.go:19（Tracer.Info 把 ...interface{} 原样转发给底层 logger）
tags: [L17, stackmap, variadic, interface, forwarding, noinline, GC, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟日志/追踪适配器里常见的变参接口转发链：caller 构造 `[]any`（含 `*token`、含指针的 struct 值、`[]*token`、字符串与 nil 接口），经 `forwarder` 接口方法进入 `layer.forward(prefix string, vals ...any)`，再以 `vals...` 转发给函数值 `func(string, ...any)`。GC 分别落在 caller、forward 帧与 sink 帧，验证多层变参切片 header/backing array 以及 interface data word 的栈位图。

`UAF_SEED` 决定每轮参数排列顺序（仅影响顺序，不影响校验和）。

# 期望行为

1. `scan(vals)` 在每层 GC 前后返回相同的 id 和最后一个 token 指针；
2. forward 把 sink 的视图与 caller 原始切片逐项对比一致，返回 `sum+layer.base`；
3. caller 在 forward 返回并再次 GC 后，原始 `args` 的每个接口值（指针、box 值、切片、字符串、nil）仍有效；
4. 调用点直接构造变参片段（不经过 `[]any` 变量）路径同样校验通过；
5. 退出码 0，最后一行 `ALL PASS l17_variadic_iface_forward`。

# 失败判读

- `token/box/seq ... corrupted`：变参 backing array 或接口 data 指针被破坏；
- `variadic slice changed across GC`：GC 前后 scan 结果不一致，说明形参切片未正确扫描；
- `caller slice changed`：caller 栈槽/切片被提前回收；
- SIGSEGV / `fatal error: found bad pointer`：漏标导致堆对象被释放；
- 仅 clobberfree 下失败：栈槽未标记为指针区。

# 参数

- `UAF_SEED`：每轮参数排列种子，失败输出带该值；
- 固定 12 轮 + 1 次直接构造变参调用。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_variadic_iface_forward/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
