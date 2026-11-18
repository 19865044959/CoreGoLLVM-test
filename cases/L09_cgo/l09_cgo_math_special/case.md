---
id: l09_cgo_math_special
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库调用边界）
  - README.md:77（L09 允许 cgo 并需标注 requires）
tags: [L09, cgo, libm, special-values, rounding, NaN, frexp]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定系统数学库时的特殊值与舍入边界：通过 C 辅助函数调用 `exp/log/log10/atan2/cbrt`（与 Go `math` 做相对/绝对容差对照），以及 `trunc/round/rint/nearbyint/frexp/modf/copysign/fmin/fmax`、`isnan/isinf/signbit`（做精确对照）。重点覆盖 NaN、±Inf、负零、半值舍入（round half away vs. rint/nearbyint round-to-even）、`frexp` 尾数/指数分解，并跨多轮显式 GC 重复调用。C 仅同步写 Go 栈上的出参，不保留 Go 指针。

# 期望行为

1. `exp/log/log10/atan2/cbrt` 的典型值与 Go `math` 在容差内一致；
2. `trunc/round/rint/nearbyint` 对正负半值与普通值逐值精确一致；
3. `frexp/modf` 的尾数、指数、整数部分与 Go 精确一致（含次正规数与 `MaxFloat64`）；
4. `isnan/isinf/signbit` 对 NaN/±Inf/普通值判定正确；
5. `copysign(0,-1)` 产生负零、`1/-0` 为 `-Inf`；
6. C `fmin/fmax` 的 NaN 语义（返回非 NaN 参数）与普通值行为正确；
7. 64 轮重复调用 + 多轮 `runtime.GC()` 后结果稳定；
8. 最后一行 `ALL PASS l09_cgo_math_special`。

# 失败判读

- 容差类断言失败：C 与 Go 数学库实现差异超过 1e-12（环境/库版本问题），或参数传递方向错误（如 atan2 的 y/x 颠倒）；
- 舍入类断言失败：C 舍入模式被修改（`rint/nearbyint` 依赖当前舍入模式），或半值语义实现差异；
- `frexp/modf` 失败：出参指针未正确写回（Go 栈指针传递问题）；
- NaN/负零失败：位模式或符号处理错误；
- GC 后失败：C 函数调用期间错误保留 Go 指针（本用例仅栈上标量出参）。
- 链接失败：缺少 `#cgo LDFLAGS: -lm`。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_math_special
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
