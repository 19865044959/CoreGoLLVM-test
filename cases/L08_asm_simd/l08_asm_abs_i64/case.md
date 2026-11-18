---
id: l08_asm_abs_i64
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/ent/entc/integration/type_test.go:65（可选 int64 字段写入 MinInt64 极值）
  - research/repos/protobuf-go/encoding/protowire/wire_test.go:652（ZigZag 对 MinInt64/MaxInt64 的对称处理）
tags: [L08, asm, amd64, abs, neg, cmov, int64, minint]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟数值处理中的绝对值热路径：`NEGQ` 求补后用 `CMOVQLT` 按符号选择，无分支；重点覆盖 `math.MinInt64`——其二补码取负仍为自身，绝对值为“负”，这是与 `math.Abs` 语义一致但容易误判的边界。

# 期望行为

1. 0、±1、±2、±7、±42、±2^62、MaxInt64、MinInt64、MaxInt64-1、MinInt64+1 的结果与 Go 参考实现 `if x<0 { -x } else { x }` 一致；
2. 500 个定种子伪随机 int64 的结果一致；
3. 除 MinInt64 外结果均为非负；MinInt64 的绝对值保持 MinInt64 且 `-abs` 回绕为自身；
4. 幂等性：`abs(abs(x)) == abs(x)`；
5. 对称性：非 MinInt64 输入满足 `abs(x) == abs(-x)`；
6. -4096..0 逐值检查 `abs(x) == -x`；
7. 每轮显式 GC + 垃圾分配后结果不变；
8. 最后一行 `ALL PASS l08_asm_abs_i64`。

# 失败判读

- 负数返回原值：`CMOVQLT` 条件或操作数方向错误，未选中 `NEGQ` 结果；
- 正数变为负数：`CMPQ` 后条件写成 `CMOVQGE`；
- MinInt64 返回 MaxInt64 或崩溃：试图对 MinInt64 做饱和/取反溢出处理，与二补码语义不符；
- 只有大数错误：立即数/寄存器宽度被截断到 32 位；
- 随机值错误但边界正确：参数槽偏移错误（ABI0 返回槽应在 `ret+8(FP)`）。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_abs_i64
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
