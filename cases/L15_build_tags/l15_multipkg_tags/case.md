---
id: l15_multipkg_tags
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_multipkg_tags/flags/mode_base.go:1 默认 !tagFast 常量/实现
  - cases/L15_build_tags/l15_multipkg_tags/flags/mode_fast.go:1 tagFast 常量/实现
  - cases/L15_build_tags/l15_multipkg_tags/engine/platform_linux.go:1 子包 GOOS 标签
  - cases/L15_build_tags/l15_multipkg_tags/engine/engine.go:13 跨包调用 flags
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, module, multi-package, custom-tags, GOOS, cross-package-link, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

module 内两个被导入包同时使用构建标签：`flags` 用自定义 `tagFast` 选择模式名与常量，`engine` 用 GOOS 标签选择平台实现，并从 `flags` 读取标签结果参与计算。默认构建（无 tags）走 slow + 当前平台；`-tags=tagFast` 走 fast。

## 期望行为

1. `flags.Default()` 的 Mode/Scale 与构建标签一致：默认 `slow/2`，tagFast 为 `fast/7`；
2. `engine.Run(5)` 的 Value 为 `5*3+Scale`（默认 17），Platform 按 GOOS 为 `linux-engine`/`other-engine`；
3. 两个包的标签选择互不干扰，跨包链接到正确实现；
4. GC 后跨包 Result 与 main payload 不变；
5. 退出码 0，最后一行 `ALL PASS l15_multipkg_tags`。

## 失败判读

- 构建找不到子包：module 路径/目录布局错误；
- Mode/Scale 与标签不符：flags 包标签文件选择错误；
- Platform 错误：engine 子包 GOOS 标签未生效；
- Value 错误：跨包链接到错误常量或符号。

## 参数

- `EXPECT_MODE=fast`：仅在 `-tags=tagFast` 构建时使用，默认 slow；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_multipkg_tags
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOTOOLCHAIN=go1.24.1 go build -tags=tagFast -o /tmp/case-fast . && EXPECT_MODE=fast /tmp/case-fast
```
