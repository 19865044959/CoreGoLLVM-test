---
id: l09_cgo_fenv_flags
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，C 数学/加密运算边界）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库调用边界）
  - docs/RUNBOOK.md:37（L09 依赖 cgo 能力标签与 C 工具链）
tags: [L09, cgo, fenv, exceptions, FE_INVALID, FE_OVERFLOW, FE_UNDERFLOW, rounding, LockOSThread, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定 libm 时对 C 浮点异常标志的依赖：C 辅助函数在 `runtime.LockOSThread` 锁定的 OS 线程上执行 `feclearexcept/fetestexcept/feraiseexcept/fesetround/fegetenv/fesetenv/FE_DFL_ENV`，用 `volatile` 操作数触发除零、NaN、溢出、下溢与仅不精确，验证标志的精确置位与累积；同时验证四种舍入模式下 1/3 的舍入方向、环境保存/恢复的原子性，以及显式 GC 后标志状态可重新清零控制这一绑定层边界。所有运算在 C 侧，C 不保留 Go 指针。

# 期望行为

1. `FE_ALL_EXCEPT` 等于五个异常掩码的按位或，默认舍入模式为 `FE_TONEAREST`，清零后无标志；
2. `1/0` 只置 `FE_DIVBYZERO` 且结果为 `+Inf`；`0/0`、`sqrt(-1)` 只置 `FE_INVALID` 且结果为 NaN；
3. `DBL_MAX*2` 置 `FE_OVERFLOW` 且伴随 `FE_INEXACT`，结果为 `+Inf`；最小次正规数 `/2` 置 `FE_UNDERFLOW` 且伴随 `FE_INEXACT`，结果为 0；
4. `1/3` 只置 `FE_INEXACT`；精确整数运算不置任何标志；两次 inexact 加一次 divzero 累积为 `INEXACT|DIVBYZERO`；
5. `feraiseexcept(INVALID|OVERFLOW)` 精确置位这两个标志；
6. 四种舍入模式可设置/读回；`FE_UPWARD` 下 1/3 等于向上相邻双精度数，`FE_DOWNWARD` 下保持略低于精确 1/3 的最近值；
7. `fegetenv/fesetenv` 同时恢复舍入模式与异常标志；`FE_DFL_ENV` 复位为最近舍入且无标志；
8. 显式 GC 后 `feclearexcept` 能清零、`feraiseexcept` 能重新精确置位（GC 自身会扰动标志，不复位则不可控）；
9. 100 轮重复触发每轮均为 `INEXACT`；退出码 0，最后一行 `ALL PASS l09_cgo_fenv_flags`。

# 失败判读

- 标志位多出/缺少：C 操作数未用 `volatile` 被常量折叠，或舍入模式/异常掩码映射错误；
- `FE_DOWNWARD/UPWARD` 方向不符：`fesetround` 未真正写入 MXCSR/x87 控制字，或 Go 侧期望值算错；
- 保存/恢复失败：`fegetenv/fesetenv` 的 `fenv_t` 生命周期或线程归属错误（goroutine 未锁定 OS 线程）；
- GC 后无法清零：fenv 被 Go 运行时或其它线程共享，检查 `LockOSThread`；
- 链接失败：缺少 `#cgo LDFLAGS: -lm`。

# 参数

- 无随机路径；1/3 重复 100 轮，GC 每 10 轮一次；`UAF_SEED` 仅由 runner 注入。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_fenv_flags
go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
