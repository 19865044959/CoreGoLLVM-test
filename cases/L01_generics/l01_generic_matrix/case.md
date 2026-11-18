---
id: l01_generic_matrix
dim: L01_generics
priority: P2
sources:
  - pgx@v5.7.4: pgtype/array_test.go:8 PostgreSQL 多维数组文本解析（维度、下界、NULL）
  - go-redis@v9.8.0: internal/proto/reader_test.go:11 RESP 嵌套数组/集合/映射的递归解析
tags: [L01, generics, matrix, numeric-constraint, transpose, multiply, algebra-identity, gc, TORTURE_ROUNDS, UAF_SEED]
requires: [generics]
runtime: "<10s（默认；TORTURE_ROUNDS 放大矩阵维度，上限 8）"
---

# 场景

泛型 `Matrix[T number]`（约束 `~int64 | ~float64`）用扁平 `[]T` 存储，提供 Fill/At/Set/Transpose/Mul/Sum/Equal/Identity。构造 A(n×m) 与 B(m×p)，填充值由 UAF_SEED 平移；在构建后、乘法后、转置后分别显式 GC。校验抽样点积、`sum(A*B)=Σ_k colSum(A,k)*rowSum(B,k)` 精确恒等式、`(A*B)^T=B^T*A^T`、转置二重还原、A*I=I*A=A、维度不匹配返回 nil。随后 `Matrix[float64]` 二次实例化，与同值 int64 矩阵逐元素精确比较。TORTURE_ROUNDS 放大维度。

# 期望行为

1. C=A*B 维度为 n×p，抽样单元等于独立点积，`sum(C)` 等于列和×行和恒等式；
2. `(A*B)^T` 与 `B^T*A^T` 逐元素相等，`C^T^T` 还原为 C，GC 后抽样回读一致；
3. A*I 与 I*A 均逐元素等于 A；维度不匹配的 Mul 返回 nil 而不 panic；
4. `Matrix[float64]` 的 6×5·5×7 结果与同值 int64 矩阵逐元素相等、总和相等；
5. 默认路径与 TORTURE 放大路径均输出 `ALL PASS`。

# 失败判读

- `dot product mismatch`：Mul 索引 `i*c+k`/`k*c+j` 或转置下标写错；
- `sum(C)` 不匹配：行/列和恒等式推导错误或数值溢出（int64 应避免极大维度）；
- `dimension mismatch` panic：Mul 前未检查 `m.c==o.r`；float 比较失败：应只使用可精确表示的小整数。

# 参数

- `TORTURE_ROUNDS`：默认 1，最大 8；n=(18+seed%4)*rounds，m=n-2，p=n+3；
- `UAF_SEED`：决定维度与填充平移量，期望值现场推导，失败输出带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_matrix/*.go
UAF_SEED=7 TORTURE_ROUNDS=4 GODEBUG=clobberfree=1 /tmp/case
```
