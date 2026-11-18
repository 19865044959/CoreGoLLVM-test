---
id: l16_constant_ops
dim: L16_template_interp
priority: P2
sources:
  - yaegi@v0.16.1: interp/interp_eval_test.go:76 TestEvalShift：常量移位与位宽
  - yaegi@v0.16.1: interp/interp_eval_test.go:87 TestOpVarConst：const/var 混合运算
  - yaegi@v0.16.1: interp/interp_test.go:195 TestGlobals：以 go/constant.Value 暴露常量
  - golangci-lint@v2.0.2: pkg/golinters/usestdlibvars/testdata/usestdlibvars.go:12 常量值 200 的取值与映射
tags: [L16, goast, go-constant, arbitrary-precision, rational, shifts, conversions, GC, TORTURE_ROUNDS]
requires: [goast]
runtime: "<30s（TORTURE_ROUNDS 放大）"
---

# 场景

直接驱动 `go/constant`：解析 Int/Float/Imag/Char/String/非法字面量，验证 `Make` 的动态类型限制、`MakeFromBytes`/`Bytes` 小端往返、1<<100 与 MaxUint64+1 的任意精度、1/3 有理数与 Num/Denom、ToInt/ToFloat/ToComplex、Float64Val 精确性、Unknown 传播、除零 panic；再用随机三元组验证加法交换/结合、减法可逆、`(a/b)*b+a%b==a`、移位等价与比较互补。所有 Value 与随机结果在 GC 后重新校验/重放。

# 期望行为

1. 字面量与转换结果与 Go 1.24 `go/constant` 一致，如 `3.5 -> 7/2`、`'A' -> 65`、`2i -> (0 + 2i)`、`ToInt(2.0) -> 2`、`ToInt(2.9) -> unknown`；
2. `1<<100` 精确为 `1267650600228229401496703205376`，`Int64Val/Uint64Val` 均报告不精确，`BitLen=101`；`MaxUint64+1` 为 `18446744073709551616`；
3. `1/3` 的 `ExactString` 为 `1/3`、Num/Denom 为 1/3；`Float64Val` 对 0.5 精确、对 1/3 不精确但返回最近双精度值；
4. 整型 `/=` 截断（7/=2→3、-7/=2→-3）、`%` 取余（-7%2→-1），普通 `/` 产生 Float（7/2）；整数除零与取余零 panic；
5. `Make` 对 bool/string/int64 返回值，对 int/uint64/float64 返回 Unknown；`Sign(unknown)=1` 用于避免伪除零；
6. 随机代数律全部成立；GC 后保留 Value 的 ExactString/Kind 与重放结果不变；
7. 最后一行 `ALL PASS l16_constant_ops`。

# 失败判读

- `lit/conv/arith ... want`：常量解析、类型提升或精确值语义错误；
- `Integer division by zero did not panic`：除零保护退化；
- `add not commutative` / `divmod identity`：任意精度运算或截断语义错误；
- GC 后 Value 值变化/崩溃：`*big.Int`/`*big.Rat` 等被回收或改写；
- 超时：`TORTURE_ROUNDS` 过大。

# 参数

- `TORTURE_ROUNDS`：默认 6 轮 × 32 组随机三元组，可放大（上限 200000）；
- `UAF_SEED`：随机三元组种子与 GC 后重放种子，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_constant_ops/*.go
TORTURE_ROUNDS=200 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=7 GODEBUG=efence=1 /tmp/case
```
