---
id: l16_types_const_overflow
dim: L16_template_interp
priority: P1
sources:
  - yaegi@v0.16.1: interp/interp_eval_test.go:76 常量移位与位宽
  - yaegi@v0.16.1: interp/interp_eval_test.go:87 TestOpVarConst：const/var 混合运算
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:289 常量类型判定与溢出报错
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:511 and/or 短路（未选分支不求值）
  - golangci-lint@v2.0.2: pkg/golinters/unconvert/testdata/unconvert.go:75 基础类型恒等转换与常量例外
  - golangci-lint@v2.0.2: pkg/golinters/usestdlibvars/testdata/usestdlibvars.go:12 常量值 200 的取值与映射
tags: [L16, goast, go-types, go-constant, arbitrary-precision, shifts, overflow, representability, GC]
requires: [goast]
runtime: "<10s"
---

# 场景

类型检查一段包含 `1<<100`、有理数 `1/3`、字符串/布尔常量、`^uint64(0)`、`uint8(255)` 与动态 `1<<k`（k 由种子和轮次决定）的合法源码，并通过 `types.Const.Val()`/`types.Info.Types` 校验精确值、Kind、类型与 var 初始化表达式的折叠结果；期望值用 `math/big` 独立计算。另有 9 个不可表示/非法常量用例：uint8/int8/float32/int64 溢出、转换溢出、除零、负移位、int+bool、多行溢出，检查错误文本与行号。对应 yaegi 的常量移位测试与 golangci-lint 的常量取值场景。

# 期望行为

1. `Base=2^100`、`Div=1024`、`Third=1/3`、`Str="ab"`、`Bool=true`、`Sh=2^63`、`Neg=-3`、`Rem=-1`、`MaxU=18446744073709551615`（uint64）、`Conv=255`（uint8）、`Pow=2^k` 的精确串、Kind 与类型全部正确；
2. `var A = Div + 24` 折叠为 `1048`（int）；`var B = Pow / 2` 为 `2^(k-1)`（int）；
3. 9 类错误均产生非 nil 诊断，含 `as uint8 value in variable declaration (overflows)`、`constant 300 overflows uint8`、`as float32 value`、`as int64 value`、`invalid operation: division by zero`、`negative shift count -1`、`mismatched types untyped int and untyped bool`、`(overflows)` 等片段，且行号分别为 2 或 3；
4. `math/big` 计算的 `2^k` 与 `types.Const` 值逐字节一致；
5. GC 后常量对象、`Info.Types` 折叠值、文件位置与所有错误文本保持不变；
6. 最后一行 `ALL PASS l16_types_const_overflow`。

# 失败判读

- 常量值/类型不符：`go/constant` 或 `go/types` 常量折叠/可表示性判断错误；
- `expected type error, got none`：溢出/非法运算未被诊断；
- `lines=[...] want`：错误位置映射错误；
- GC 后 `Info.Types` 丢失 / 常量值变化：类型检查结果对象被破坏；
- `independent big.Int mismatch`：期望值生成路径自身异常。

# 参数

- `UAF_SEED`：决定轮数（3–5）与移位位数 k（60–63），失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_types_const_overflow/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GODEBUG=clobberfree=1 /tmp/case
```
