---
id: l17_fca_nested_defer_arg
dim: L17_stackmap
priority: P2
sources:
  - sonic@v1.13.2: decode_test.go:2433（TestUnmarshalMaxDepth 深层嵌套聚合）
  - ent@v0.14.4: dialect/gremlin/encoding/graphson/struct_test.go:83（TestEncodeNestedStruct 自引用嵌套 struct）
tags: [L17, stackmap, FCA, nested-aggregate, defer-record, byval-return, any-field, TORTURE_ROUNDS, GC, UAF_SEED]
requires: []
runtime: "<10s（默认 32 轮；TORTURE_ROUNDS 放大轮数，上限 1024）"
---

# 场景

`grid` 是由 `[2]row`、`row{[2]leaf, any}`、嵌套 `subAgg{[2]row, []*item}`、`[3]*item` 和 any 组成的嵌套 FCA 聚合。`makeGrid` 按值返回它；`run` 在 `defer takeGrid(g, ...)` 处整体复制进 defer 记录，随后清零局部 `g` 并 GC，再把另一个 `grid` 作为命名返回值按值返回；defer 内部两轮 GC 并逐字段校验。另覆盖按值传入 `mutateByVal` 后 caller 副本不受影响、以及直接把聚合返回值作为实参传给 `takeGrid`。

`UAF_SEED` 决定每轮 base 与 grid 顶层 any 的装箱形态；`TORTURE_ROUNDS` 放大轮数。

# 期望行为

1. defer 记录中的嵌套聚合在局部清零并 GC 后仍完整，内部两轮 GC 结果相同；
2. `takeGrid` checksum 等于 `gridSum(base)`；
3. `run` 的命名返回 `out` 在 deferreturn 与 caller GC 后完整，checksum 等于 `gridSum(base+1)`；
4. `mutateByVal` 只改自己的副本：caller 的 grid checksum 不变，返回的 grid 中替换后的指针生效；
5. 直接聚合实参路径 checksum 正确；
6. 退出码 0，最后一行 `ALL PASS l17_fca_nested_defer_arg`。

# 失败判读

- `FCA defer record changed across GC`：聚合 defer 记录漏扫或局部清零导致副本被提前回收；
- `leaf/anyv/list/ptrs corrupted`：嵌套聚合内的指针字段被破坏；
- `checksum ... want`：聚合拷贝/返回值不完整；
- `by-value callee mutated caller grid`：按值传递被错误实现为共享；
- 仅 clobberfree 下失败：defer 记录或返回槽中的 FCA 未标记。

# 参数

- `UAF_SEED`：每轮 base 与 any 形态种子，失败输出带该值；
- `TORTURE_ROUNDS=N`：默认 32 轮，上限 1024。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_fca_nested_defer_arg/*.go
UAF_SEED=1 TORTURE_ROUNDS=128 GODEBUG=clobberfree=1 /tmp/case
```
