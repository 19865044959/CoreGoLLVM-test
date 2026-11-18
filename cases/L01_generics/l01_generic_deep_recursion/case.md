---
id: l01_generic_deep_recursion
dim: L01_generics
priority: P2
sources:
  - gorm@v1.30.0: schema/schema_test.go:18 递归遍历匿名嵌入字段，字段路径为前缀链，深度可放大到 10k 层测栈
  - gin@v1.10.0: gin_test.go:538 路由内递归 HandleContext 1 万次，中间件/handler 的 atomic 计数均为 10000
  - pgx@v5.7.4: pgtype/multirange_test.go:20 Multirange[Range[Int4]] 的嵌套泛型实例化
tags: [L01, generics, deep-recursion, stack-growth, generic-frame, pointer-liveness, gc, TORTURE_ROUNDS, UAF_SEED]
requires: [generics]
runtime: "<3s（默认深度 2.5k–4k；TORTURE_ROUNDS≤16 约 0.1s）"
---

# 场景

构建数千到数万层的泛型 `link[cell]` 深链与 341 节点的平衡泛型 `tree[cell]`，
用递归泛型函数 `chainSum`/`treeStats` 下降，并在递归内部按深度/节点数调用
`runtime.GC()`；随后反转链再次递归，另以 `link[int64]` 第二实例化验证不同 T 的
递归帧。对应 gorm 匿名嵌入深递归、gin 递归 HandleContext 1 万次、pgx 嵌套泛型。

# 期望行为

1. 深链长度、头/尾 depth 正确，递归求和等于预计算和；
2. 递归中多次 GC 后，抽样 `*cell` 的指针、V、Tag 完整；
3. 反转链后头/尾 depth 正确，深递归求和不变，GC 后样本仍有效；
4. nil 链递归返回 0，单元素链的构建/求和/反转边界正确；
5. `link[int64]` 第二实例化的长度与求和正确；
6. 平衡树节点数与预计算一致，递归遍历节点数/载荷和正确，前序 id 与 payload 一致；
7. 最终 GC 后树根载荷不变；
8. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_generic_deep_recursion`。

# 失败判读

- `deep chain sum mismatch` / `reversed chain sum mismatch`：深递归帧中的参数或指针被破坏；
- `sampled chain ... corrupted after GC`：栈增长/栈复制时泛型帧的指针位图漏标；
- `chain length/depth mismatch`：构建或反转链的指针操作错误；
- `tree traversal node count mismatch` / `tree payload ...`：树递归或 payload 指针布局错误；
- `fatal error: stack overflow`：递归深度或栈上限异常（用例默认远低于限制）；
- `FAILED ... seed=`：失败输出带 UAF_SEED。

# 参数

- `UAF_SEED`：决定链深度（2500..3999）与载荷偏移（0..88），失败输出带种子；
- `TORTURE_ROUNDS`：链深度倍数（默认 1；上限 16，最深约 6.4 万层）。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_generic_deep_recursion/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
