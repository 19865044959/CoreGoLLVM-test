---
id: l09_cgo_libm_math
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库调用边界）
tags: [L09, cgo, libm, float, math]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定系统数学库：通过 C 辅助函数调用 sqrt/hypot/pow/sin/cos/fmod/floor/ceil/fabs/ldexp，与 Go math 包结果对照（浮点用相对/绝对容差）。

## 期望行为

1. sqrt(2)/sqrt(144)/hypot(3,4)/pow(2,10)/pow(2,-0.5)/sin(pi/2)/cos(0)/fmod(17,5) 在容差内一致；
2. floor(-2.5)=-3、ceil(-2.5)=-2、fabs(-7.25)=7.25；
3. ldexp 在 e=-10..40 与 math.Ldexp 一致（含负指数）；
4. 8 个基准值 × 64 轮重复调用，每 16 轮 GC，结果稳定；
5. 最后一行 `ALL PASS l09_cgo_libm_math`。

## 失败判读

- 链接失败：缺少 `#cgo LDFLAGS: -lm` 或 C 工具链；
- 负指数 ldexp 错误：把 double 结果截断为整数；
- 浮点比较失败：容差过严或 C 函数返回值转换错误；
- GC 后结果变化：浮点调用与 GC 无共享状态，若发生应排查 cgo 调用点栈映射。

## 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=cgo` 可过滤本用例。

## 运行

```bash
cd cases/L09_cgo/l09_cgo_libm_math
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
