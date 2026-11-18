---
id: l15_module_var_deps
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_module_var_deps/seed/seed.go:11 seed.Base 包变量
  - cases/L15_build_tags/l15_module_var_deps/calc/calc.go:8 calc 变量依赖 seed
  - cases/L15_build_tags/l15_module_var_deps/calc/calc.go:12 var 初始化调用函数建表
  - cases/L15_build_tags/l15_module_var_deps/report/report.go:9 report 依赖 calc+seed
  - cases/L15_build_tags/l15_module_var_deps/trace/trace.go:9 init 顺序记录
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, module, multi-package, package-variable, init-order, dependency-chain, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

module 内 `trace <- seed <- calc <- report <- main` 的多包变量依赖链：calc 的包级变量引用 seed.Base，report 的变量引用 calc 与 seed，calc.Table 在 var 初始化阶段调用函数构建；每个包 init 向 trace 记录顺序。验证跨包变量初始化依赖与顺序。

## 期望行为

1. seed.Base=6；calc.Doubled=12、calc.Plus=7、calc.Table=[6,12,7]；report.Total=25；mainBase=13；
2. report.Items 链接 seed.Node，长度 2；
3. init 顺序：trace 最先，且 trace<seed<calc<report<main，共 5 个标记；
4. GC 后 seed 节点链与 report.Items 指针仍有效；
5. 退出码 0，最后一行 `ALL PASS l15_module_var_deps`。

## 失败判读

- 变量值错误：跨包包级变量初始化顺序/依赖解析错误；
- Table 未按函数结果初始化：var 初始化调用未执行或求值顺序错误；
- init 顺序违反依赖边：包初始化调度错误；
- GC 后指针损坏：跨包全局变量写屏障/栈映射问题。

## 参数

`BUILD_MODE=module`；无自定义 tag，`go.mod` 声明 `go 1.24`。

## 运行

```bash
cd cases/L15_build_tags/l15_module_var_deps
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
