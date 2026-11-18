---
id: l17_embed_wrapper_aggregate
dim: L17_stackmap
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1493（消息 struct 嵌入 MessageState 等 wrapper 字段，跨 GC 保留）
  - fasthttp@v1.59.0: fasthttpadaptor/adaptor_test.go:16（TestNewFastHTTPHandler 经 http.Handler 接口适配）
tags: [L17, stackmap, embedding, wrapper, promoted-method, aggregate-by-value, interface, GC]
requires: []
runtime: "<10s（默认 8 轮）"
---

# 场景

三层嵌入 `wrapC{wrapB{wrapA{core}}}`，`core` 带指针/切片字段，`total`/`Peek` 经嵌入链提升。用例把整个 `wrapC` 按值传给 noinline `useC`、把 `wrapB` 按值传给 `useB`，并把 `wrapC` 值（及指针）装箱进 `sizer` 接口后调用 promoted 方法；每次调用内 GC，返回后 caller 再 GC 并校验嵌入路径上的全部字段。另用方法表达式 `wrapB.total` 复用同一 wrapper 调整逻辑。

# 期望行为

1. `useB`/`useC` 返回的求和等于按嵌入链算出的期望值；
2. 值装箱与指针装箱两种 `sizer` 路径在 GC 后返回相同结果，`Peek` 返回的 head 指针有效；
3. 方法表达式 `wrapB.total(w.wrapB, ...)` 结果正确；
4. 每次调用后 GC，`w`/`w.wrapB.extra`/`w.list` 等字段不变；
5. 最后一行 `ALL PASS l17_embed_wrapper_aggregate`，退出码 0。

# 失败判读

- `useB/useC` 结果错误：按值聚合参数或嵌入偏移处的指针槽漏标；
- `iface ... Peek` 失败：接口装箱副本中的指针未被扫描；
- 仅 clobberfree 下失败：嵌入 wrapper 持有的对象被提前回收；
- 崩溃：promoted method 的 receiver 偏移计算或 wrapper 栈映射错误。

# 参数

- `UAF_SEED`：决定 base 偏移与 list 长度；失败信息带种子。
- 无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_embed_wrapper_aggregate/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
