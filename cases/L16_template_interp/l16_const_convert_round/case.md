---
id: l16_const_convert_round
dim: L16_template_interp
priority: P1
sources:
  - yaegi@v0.16.1: extract/extract_test.go:46 constant.MakeFromLiteral 处理 MaxFloat32 字面量
  - yaegi@v0.16.1: interp/interp_test.go:229 constant.Value 接口与 MakeInt64 精确比较
  - golangci-lint@v2.0.2: pkg/golinters/nolintlint/internal/nolintlint_test.go:281 单文件解析驱动编译期检查
tags: [L16, goast, go-constant, go-types, rounding, float32, float64, overflow, GC]
requires: [goast]
runtime: "<10s"
---

# 场景

模拟 yaegi 抽取常量表、golangci-lint 做编译期常量检查：先用 `go/types.Info.Types`
从 const 声明取出精确值，再用 `go/constant` 观察 float32/float64 舍入开销标志，最后与
同一表达式在运行期转换出的位模式对齐。重点是半程 ties-to-even、次正规、溢出为 Inf、
2^24/2^53 附近的整数转浮点，以及 int64 可表示性边界。

## 期望行为

1. 7 个 const 全部取到值，浮点常量类型为 untyped float、整数为 untyped int；
2. `1.0/3.0` 精确值为 `1/3`，`MaxI` 精确落入 int64，`OverI` 不落入且位长 64；
3. `ToInt(2.5)` 与 `ToInt(Tie)` 返回 Unknown；
4. `float32(1+2^-24)` 舍入为 1.0（ties-to-even），位模式 `0x3f800000` 且 exact=false；
5. `float32(1.4e-45)` 为最小次正规 `0x00000001`，`float32(1e-46)` 下溢为 0；
6. `float32(1e39)` 为 +Inf 且 exact=false，`float32(16777217)` 舍入到 `0x4b800000`；
7. `float64(2^53+1)` 舍入到 2^53（`0x4340000000000000`）且 exact=false；
8. 运行期 `float32/float64` 转换的位模式与 `go/constant` 的舍入结果逐位一致；
9. 3 轮 GC 后常量值、类型对象仍可读取，按 `UAF_SEED` 打乱复核顺序不丢名字。

## 失败判读

- `... bits=...`：go/constant 或编译器常量舍入方向/精确标志错误；
- `runtime ... mismatch`：编译期常量折叠与运行期转换语义不一致；
- `kind=/type=`：`types.TypeAndValue` 中常量种类/类型丢失；
- `lost ...` / `changed`：常量值或类型对象在 GC 后失效。

## 参数

- `UAF_SEED`：控制每轮 GC 后常量复核与重算顺序；失败输出带种子；
- 无需其他 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_const_convert_round/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
