---
id: l15_main_arch_entry
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_main_arch_entry/main_amd64.go:5 amd64 入口常量
  - cases/L15_build_tags/l15_main_arch_entry/main_amd64.go:11 amd64 入口 main
  - cases/L15_build_tags/l15_main_arch_entry/main_arm64.go:11 arm64 入口 main
  - cases/L15_build_tags/l15_main_arch_entry/main_generic.go:1 其余 GOARCH 兜底入口
  - cases/L15_build_tags/l15_main_arch_entry/main.go:22 共享变量引用入口常量
  - cases/L15_build_tags/l15_main_arch_entry/main.go:52 入口校验
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, GOARCH, filename-suffix, main-entry, mutual-exclusion, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

`main` 函数本身由 GOARCH 文件名后缀选择：`main_amd64.go`、`main_arm64.go` 各提供
一个入口，其余 GOARCH 由带 `!amd64 && !arm64` 的 `main_generic.go` 兜底。每个入口
定义自己的入口名/偏置常量与 init 标记；共享文件的包级变量 `derivedBias`、`payload`
引用被选中入口的常量。链接期必须恰好一个 main，运行期入口名、偏置、init 与变量
初始化必须一致。

## 期望行为

1. amd64 入口为 `("amd64", 11)`；arm64 为 `("arm64", 7)`；其余为 `("generic", 3)`；
2. 入口 init 标记恰好 1 个，等于 `entry-<选中平台>`；
3. `derivedBias == bias+1`，`payload.name/bias` 与入口一致（在 main 前已完成）；
4. 两次 GC 后入口选择、init 标记与派生变量不变；
5. 退出码 0，最后一行 `ALL PASS l15_main_arch_entry`。

## 失败判读

- `main redeclared` / `multiple definition of main`：后缀与兜底 tag 未互斥；
- `function main is undeclared`：所有入口都被排除；
- 入口名/偏置不符：选中了另一 GOARCH 的 main；
- init 标记数大于 1：多个入口文件被链接；
- `derivedBias` 不符：共享变量未引用被选中入口的常量。

## 参数

- 无额外参数；`BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_main_arch_entry
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOARCH=arm64 GOTOOLCHAIN=go1.24.1 go build -o /tmp/case.arm64 .
GOARCH=386 GOTOOLCHAIN=go1.24.1 go build -o /tmp/case.386 .
```
