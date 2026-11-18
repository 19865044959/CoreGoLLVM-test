---
id: l17_iface_defer_variadic_edge
dim: L17_stackmap
priority: P0
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/examplefiles_test.go:94（defer func(dirs ...string) 以多个实参注册）
  - nats-server@v2.11.0: server/client_test.go:1634（Warnf(format string, v ...any) 接口式变参日志方法）
tags: [L17, stackmap, defer, interface-method, variadic-boundary, nil-slice, method-value, mutated-backing-array, GC, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

`pusher.Push(tag, vals ...any)` 与 `appender.Append(vals ...*item)` 都由 `*impl` 实现。一次性注册 9 个 defer：Push 覆盖零实参、nil 展开、非 nil 空展开、1 实参、13 实参、以及注册后原地修改 backing array 的 3 实参；Append 覆盖零实参、nil 展开与 2 元素展开。注册后把两个接口变量重绑定到另一实现（tag=rebound、multi=999）。每个方法内执行两轮 GC，并把 `tag:sum` 写入 deferred 记录；main 按期望集合逐一核对。

`UAF_SEED` 决定每轮 base。

# 期望行为

1. 9 条 defer 全部执行且仅执行一次，记录集合与期望完全匹配；
2. 旧 receiver（tag=orig、multi=1/2）生效，重绑定后的实现未被使用；
3. nil 展开与 `[]any{}` 展开区分：仅 nil 记录 1<<40/1<<41 标记；
4. 注册后对 mut backing array 的原地修改对 defer 可见（值 `base+99` 与 `tail-mut`）；
5. 每个方法 GC 前后变参切片 sum 不变，receiver keep 指针仍有效；
6. 退出码 0，最后一行 `ALL PASS l17_iface_defer_variadic_edge`。

# 失败判读

- `deferred receiver corrupted`：defer 记录中的接口 receiver 未被扫描或重绑定语义错误；
- `variadic slice changed across GC`：defer 记录中的变参切片头/元素指针失效；
- `deferred record ... count`：某条 defer 丢失/重复，或 sum 与期望不符；
- `deferred calls ran before return`：defer 提前执行；
- 仅在 clobberfree 下失败：defer 记录中的旧 receiver 或 backing array 漏标。

# 参数

- `UAF_SEED`：每轮 base 种子，失败输出带该值；
- 固定 3 轮 × 9 个 deferred 调用。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_iface_defer_variadic_edge/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
