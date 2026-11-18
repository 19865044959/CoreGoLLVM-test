---
id: l17_iface_defer_variadic
dim: L17_stackmap
priority: P0
sources:
  - grpc-go@v1.71.1: stats/opencensus/e2e_test.go:301（defer view.Unregister(allViews...) 变参 defer）
  - badger@v4.5.0: logger_test.go:30（接口实现可变参 Errorf，内部 v... 转发）
tags: [L17, stackmap, defer, interface-method, variadic, defer-record, method-expression, GC, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

`flusher` 接口的方法是变参的 `Flush(prefix string, vals ...any)`，分别由指针接收者 `*impl` 与值接收者 `valImpl` 实现。`useDirect` 先构造 `[]any`（含 `*item`、box、`[]*item`、字符串、nil），在 `defer f.Flush("direct", args...)` 处复制接口 receiver 和变参切片头；随后原地修改 args 的 backing array、把接口变量重绑定到另一个实现并 GC。deferreturn 时方法必须使用旧 receiver 和修改后的切片。`useExpr` 走接口方法表达式 `flusher.Flush` 再 defer。

`UAF_SEED` 决定每轮 keep 值、实现选择与失败输出。

# 期望行为

1. defer 记录中的旧 receiver 生效：返回 checksum 使用原实现的 `keep`，不是重绑定值；
2. 变参 backing array 的原地修改对 defer 可见（`"mutated"`、`"tail-mutated"`、`"expr-mutated"`）；
3. 方法内两轮 GC 前后变参扫描结果一致，值接收者副本中的指针仍有效；
4. 每次调用恰好产生一条 deferred sum，16 条总和与按索引期望一致；
5. 退出码 0，最后一行 `ALL PASS l17_iface_defer_variadic`。

# 失败判读

- `receiver corrupted` / `wrong receiver tag`：defer 记录中 interface data 指针被回收或重绑定语义错误；
- `variadic defer record changed across GC`：defer 记录中的变参切片未被扫描；
- `deferred sum ... want ...`：变参内容或 receiver 错误（含原地修改未生效）；
- `deferred method did not run`：defer 记录丢失；
- SIGSEGV：接口 receiver 或切片元素指针失效。

# 参数

- `UAF_SEED`：每轮 keep/实现选择种子，失败输出带该值；
- 固定 8 轮 × 2 种 defer 形态（直接调用 + 方法表达式）。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_iface_defer_variadic/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
