---
id: l17_variadic_iface_boundary
dim: L17_stackmap
priority: P1
sources:
  - gorm@v1.30.0: tests/connpool_test.go:27（ExecContext 的 args ...interface{} 原样转发给下游）
  - grpc-go@v1.71.1: stats/opentelemetry/csm/observability_test.go:427（拦截器把 opts ...grpc.CallOption 透传）
tags: [L17, stackmap, variadic, interface-forwarding, nil-spread, reslice, boundary, GC, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

`tailSink`、`layerB`、`layerA` 组成三层变参接口转发链，每层形参都是 `...any`。输入覆盖 0 实参、1 实参、13 实参（强制溢出）、`nil []any` 展开、非 nil 空切片展开与非空切片展开。每层在转发前后执行两轮 `runtime.GC()` 并重扫变参切片；B 层额外校验 `vals[:0]`/`vals[1:]` 重切片别名，sink 用额外标记区分 nil 与空切片。

`UAF_SEED` 决定每轮 base 与失败输出。

# 期望行为

1. 三层转发后的 checksum 等于 `scanArgs(vals) + len(vals)*100 + nilMarker + 3`；
2. 每一层 GC 前后重扫同一变参切片的 sum 不变，接口 data word 可解引用；
3. B 层重切片长度与内容别名正确，`vals[:0]` 不越界；
4. sink 能将 nil 展开与 `[]any{}` 展开区分开（nil marker 仅在 nil 时出现）；
5. caller 传入的 `[]any` 在调用与 GC 后长度、内容、tail 指针均不变；
6. 零实参直接调用不 panic，tail 为 nil；
7. 退出码 0，最后一行 `ALL PASS l17_variadic_iface_boundary`。

# 失败判读

- `sink/b/a: variadic slice changed across GC`：某一层对变参切片的栈槽/接口值扫描错误；
- `reslice ...`：重切片长度或别名异常，通常是对 nil/空切片边界处理错误；
- `caller args changed`：caller 的 []any backing array 被 callee 破坏；
- `tail pointer mismatch` / `tail ... corrupted`：接口 data 指针失效；
- SIGSEGV：变参切片内 *item/pair 指针被提前回收。

# 参数

- `UAF_SEED`：每轮 base 种子，失败输出带该值；
- 固定 8 轮，每轮覆盖 nil / 空 / 1 实参 / 13 实参 / 非 nil 空切片，并额外做一次零实参调用。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_variadic_iface_boundary/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
