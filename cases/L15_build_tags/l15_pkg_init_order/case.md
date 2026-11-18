---
id: l15_pkg_init_order
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_pkg_init_order/trace/trace.go:9 trace 包 init 记录顺序
  - cases/L15_build_tags/l15_pkg_init_order/base/base.go:5 base 包变量依赖 trace
  - cases/L15_build_tags/l15_pkg_init_order/left/left.go:8 left 变量依赖 base
  - cases/L15_build_tags/l15_pkg_init_order/right/right.go:8 right 变量依赖 base
  - cases/L15_build_tags/l15_pkg_init_order/main.go:29 main 包 init 记录
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, module, multi-package, init-order, package-variable, dependency-order, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

module 的导入关系为 `trace <- base <- {left,right} <- main`。每个包在 init 中向共享 trace 包追加自己的标记；main 的包级变量还依赖 left/right/base 的变量。验证依赖包先于依赖者初始化，且 main 在全部导入包之后初始化。

## 期望行为

1. 共 5 个 init 标记，trace/base/left/right/main 各恰好 1 次；
2. `trace` 最先；`base` 在 `trace` 之后；`left`、`right` 都在 `base` 之后；`main` 在 base/left/right 之后；
3. left 与 right 相互独立，不断言二者相对顺序；
4. 包变量值：base.Value=21，left.Derived=42，right.Derived=22，mainValue=85；
5. GC 后值仍正确；
6. 退出码 0，最后一行 `ALL PASS l15_pkg_init_order`。

## 失败判读

- init 标记数量/重复：包初始化被执行多次或漏执行；
- 依赖包晚于依赖者：init 调度/依赖图排序错误；
- main 早于导入包：跨包初始化顺序错误；
- 变量值错误：包级变量初始化链断裂。

## 参数

`BUILD_MODE=module`；不依赖自定义 tag。

## 运行

```bash
cd cases/L15_build_tags/l15_pkg_init_order
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
