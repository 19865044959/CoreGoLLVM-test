---
id: l11_const_divmod
dim: L11_compiler_opts
priority: P0
sources:
  - fasthttp@v1.59.0: bytesconv_64_test.go:18 TestAppendUint 十进制格式化中的 /10、%10
  - fasthttp@v1.59.0: round2_64_test.go:10 TestRound2ForSliceCap 容量取整的移位/掩码常量运算
  - klauspost-compress@v1.17.11: compressible_test.go:9 BenchmarkEstimate 字节直方图与熵估算中的除法
tags: [const-fold, division, modulo, magic-number, signed-truncation, wrapping, panic-recover, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 URL/整数格式化与压缩熵估算中的常量除数优化：13 个常量除数（含 2 的幂、非 2 的幂与负数）的 `/`、`%` 与运行期除数参考函数逐项对照，覆盖 `MinInt64`、`MaxInt64`、`±2^62` 等边界；验证有符号商向零截断、余数符号随被除数、`MinInt64/-1` 回绕为自身且余数为 0；无符号 7 个除数覆盖高位置位值；int8 全值域与 int32 边界覆盖窄宽度符号扩展。随机 128 组值的结果在 GC 后重算复核。除零/模零必须 panic 且为 `runtime.Error`。

# 期望行为

1. 全部常量除数组合的商/余数与运行期除数参考一致；
2. `q*d + r == x` 恒等式成立（`MinInt64/-1` 回绕项单独校验）；
3. 负数除法向零截断、余数与被除数同号；
4. 无符号常量除数结果与参考一致且恒等式成立；
5. int8 全部 256 个值与 int32 边界的常量除法与参考一致；
6. 随机值结果在 GC 前后重算一致；
7. 除以零、模以零、无符号除零均 panic 且为 `runtime.Error`；
8. 最后一行输出 `ALL PASS l11_const_divmod`。

# 失败判读

- `const division/modulo`：魔数乘法/移位取模实现错误（常见为负数或高位置位时）；
- `q*d+r identity`：商余数组合不再还原被除数；
- `MinInt64 / -1`：溢出回绕语义错误；
- `negative dividend/divisor`：截断方向或余数符号错误；
- `unsigned const division`：无符号高位处理错误；
- `int8/int32`：窄宽度符号扩展/截断错误；
- `expected divide-by-zero panic`：除零未 panic 或非 runtime.Error。

# 参数

- `UAF_SEED`：决定 128 组随机被除数，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P0，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_const_divmod/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_const_divmod/*.go
UAF_SEED=1 /tmp/case_n
```
