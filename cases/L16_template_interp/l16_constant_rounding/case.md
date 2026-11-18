---
id: l16_constant_rounding
dim: L16_template_interp
priority: P1
sources:
  - yaegi@v0.16.1: interp/interp_eval_test.go:76 常量移位与有符号/无符号位宽转换
  - yaegi@v0.16.1: interp/interp_eval_test.go:40 算术/比较/一元表达式求值，含 untyped int/float 混合与除零
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:289 常量类型判定与溢出报错
  - gitea@v1.23.5: modules/templates/eval/eval_test.go:43 int 与 float 混合提升为 float64
tags: [L16, goast, go-constant, arbitrary-precision, rounding, float32, exact-compare, GC]
requires: [goast]
runtime: "<10s"
---

# 场景

直接驱动 `go/constant` 的精确值与舍入语义：INT/FLOAT/CHAR/STRING/十六进制浮点字面量、`ToInt` 的可表示性（3.0 可转、2.5 返回 Unknown）、有理数 1/3 与 float64(1/3) 的精确比较、`Float32Val`/`Float64Val` 的 exact 标志、0.1 的精确有理数表示、`ToFloat`/`ToComplex`、复数实部/虚部、1<<63 的 Int64/Uint64 边界、`Shift`/`UnaryOp`/`Unknown` 传播。对应 yaegi 的常量移位与 gitea 的数值提升测试。

# 期望行为

1. `42` 为 Int、BitLen 6、Sign +1；`-7` Sign -1；`3.0` 的 ExactString 为 `3`，`ToInt` 得 Int 3；`2.5` 的 `ToInt` 为 Unknown；`-0.0` 转 Int 0；
2. `'a'` 为 Int 97；`"hé"` 的 StringVal 正确，字符串 ExactString 带引号；`0x1p-2` 精确为 `1/4`；
3. 1/3 的 ExactString 为 `1/3`、Num/Denom 为 1/3，且精确大于 float64(1/3)；int 1 与 float 1.0 相等、1 < 1.5；字符串/布尔比较正确；
4. 1/3 的 Float64Val/Float32Val 均非精确且等于 `float64(1/3)`/`float32(1/3)`；0.5 两者精确；π 的 float64 精确、float32 非精确；0.1 的精确值为 `3602879701896397/36028797018963968`；
5. `ToFloat(7)` 为 Float 7，`ToComplex(7)` 实/虚为 7/0；3+4i 的 ExactString 为 `(3 + 4i)`，实/虚均为 Int；
6. 1<<100 的 BitLen 为 101，右移 100/99 得 1/2；Int64Val(1<<63) 返回 MinInt64 且非精确，Uint64Val(1<<63) 精确，Uint64Val(1<<64) 为零且非精确；`^1=-2`、`^-1=0`、`-5`、`+5`、`!true=false`；
7. Unknown 经二元/一元运算/ToFloat 传播后仍为 Unknown；
8. 所有保留 Value 在三轮 GC 后 Kind/ExactString/Int64Val 不变；末行输出 `ALL PASS l16_constant_rounding`。

# 失败判读

- `FAIL: ... exact=`：常量精确值/舍入语义变化；
- `FAIL: ToInt(2.5) must be Unknown`：可表示性判断错误；
- `FAIL: Int64Val/Uint64Val`：位宽边界处理异常；
- GC 轮次失败：常量内部表示被回收或破坏。

# 参数

- `UAF_SEED`：决定每轮重建常量与复核顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_constant_rounding/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
