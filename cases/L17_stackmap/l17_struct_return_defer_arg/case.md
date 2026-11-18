---
id: l17_struct_return_defer_arg
dim: L17_stackmap
priority: P0
sources:
  - badger@v4.5.0: db_test.go:104（getTestOptions 按值返回含函数字段的大 Options struct）
  - quic-go@v0.50.1: mock_quic_conn_test.go:161（ConnectionState 按值返回聚合结构）
tags: [L17, stackmap, aggregate-return, struct-by-value, defer-record, noinline, GC, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

`mkBundle` 按值返回一个含 `*item`、`[3]*item`、嵌套 subAgg（指针+切片）、字符串切片和 `any` 字段的聚合。调用方先把返回值作为普通实参校验，再在 `defer takeBundle(b, ...)` 语句处把整个聚合复制进 defer 记录；随后把局部变量 `b` 清零并 GC。defer 执行时只能依赖 defer 记录中的副本，且 defer 内部再做两轮 GC 与校验。另覆盖按值返回后立刻跨 GC 使用、以及一次返回两个 struct 的路径。

`UAF_SEED` 决定每轮 base 与 `anyv` 的装箱形态（`*item` 或值 struct）。

# 期望行为

1. `mkBundle` 返回的每个指针字段与切片元素在 GC 前后一致；
2. defer 记录中的聚合一（`bundle`）在局部变量清零并 GC 后仍完整，defer 内两轮 GC 结果相同；
3. `takeBundle` 累加的 checksum 等于 `bundleSum(base)`；
4. `useReturned` 的 struct 局部跨 GC 后 checksum 正确，返回的 `*item` 字段仍可解引用；
5. `splitReturn` 返回的两个 struct 在 caller GC 后都正确；
6. 退出码 0，最后一行 `ALL PASS l17_struct_return_defer_arg`。

# 失败判读

- `defer-record copy changed across GC`：defer 记录中的聚合栈槽未被扫描或被提前回收；
- `field/checksum corrupted`：返回槽或聚合内部的指针被破坏；
- 清零局部后仍失败：说明实现错误地复用了局部变量而非 defer 副本（预期如此设计，用于暴露错误代码）；
- 仅 clobberfree 下失败：defer 记录所在栈槽漏标；
- SIGSEGV：聚合中的指针失效。

# 参数

- `UAF_SEED`：每轮 base 与 `anyv` 形态种子，失败输出带该值；
- 固定 4 轮 × 3 种返回路径。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_struct_return_defer_arg/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
