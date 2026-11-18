---
id: l09_cgo_math_precision
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库调用边界）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，C 数学/加密运算边界）
tags: [L09, cgo, libm, fenv, rounding, long-double, IEEE-754, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定系统数学库时对浮点环境的依赖：C 辅助函数切换 `FE_TONEAREST/UPWARD/DOWNWARD`，验证半 ULP（`1+2^-53`）与 `1/3` 除法在各舍入模式下的结果；同时探测 `float/double` 的 IEEE-754 位模式与 `long double` 扩展尾数（x87 80-bit）。所有参数/返回值均为标量，C 不保留 Go 指针。

# 期望行为

1. `DBL_MANT_DIG=53`、`FLT_MANT_DIG=24`，`LDBL_MANT_DIG>=64` 且 `sizeof(long double)>=10`；
2. 初始舍入模式为 `FE_TONEAREST`；
3. 向上舍入时 `1+2^-53` 变为 `1+2^-52`，`-1-2^-53` 保持 -1；向下舍入时相反；
4. 恢复最近偶数后两者分别回到 1.0 与 -1.0；
5. `1/3` 向上舍入等于最近结果的下一个 double，向下舍入等于最近结果；最近结果 ×3 < 1；
6. `0.1` 与 `1/3` 的 float32/float64 位模式与 IEEE-754 常量一致；
7. `long double` 探针保留 1e-19 量级信息（double 会丢失），`LDBL_EPSILON` 位于扩展精度范围；
8. 显式 GC 后舍入模式、位模式与 long double 探针不变；
9. 最后一行 `ALL PASS l09_cgo_math_precision`。

# 失败判读

- 尾数/尺寸断言失败：目标平台 `long double` 未提供扩展精度（用例面向 linux/amd64 x87）；
- `upward/downward/nearest`：C 运算是常量折叠绕过运行时舍入模式，或 `fesetround` 未生效；
- `1/3` 断言：当前舍入模式与预期方向不一致，或 libc 除法未正确舍入；
- 位模式失败：目标非 IEEE-754/小端；
- `long double probe`：x87/SSE 精度配置不同或探针被优化掉；
- 链接失败：缺少 `#cgo LDFLAGS: -lm`。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`；linux/amd64 + glibc + x87 extended long double。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_math_precision
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
