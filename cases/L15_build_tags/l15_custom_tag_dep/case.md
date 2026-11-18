---
id: l15_custom_tag_dep
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_custom_tag_dep/feat_on.go:1 tagFeat 实现依赖
  - cases/L15_build_tags/l15_custom_tag_dep/helper_on.go:1 同 tag 依赖文件（函数/常量/init）
  - cases/L15_build_tags/l15_custom_tag_dep/feat_off.go:1 默认互补实现
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, custom-tags, tag-dependency, init]
requires: [pkgbuild]
runtime: "<2s"
---

# 场景

`tagFeat` 打开时，`featImpl` 依赖同一构建标签下的 `helper_on.go`（`featHelper()` 函数、`featConst` 常量、`init` 标记）；默认构建必须只编译 `feat_off.go`，完全排除该依赖文件。

## 期望行为

1. 默认构建 `featImpl()=="base"` 且 `helperInit==false`；
2. `-tags=tagFeat` 构建 `featImpl()=="feat+linked:gated"` 且 `helperInit==true`；
3. GC 后实现值与 payload 指针不变；
4. 退出码 0，最后一行 `ALL PASS l15_custom_tag_dep`。

## 失败判读

- 默认构建未定义 `featHelper`/`featConst` 而链接失败：依赖文件未被正确排除；
- 带 tag 构建符号缺失：同 tag 依赖文件未参与编译；
- `helperInit` 与实现不一致：init 选择与 impl 选择不同步。

## 参数

- `EXPECT_FEAT=on`：仅在 `-tags=tagFeat` 构建时使用，默认 base；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_custom_tag_dep
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOTOOLCHAIN=go1.24.1 go build -tags=tagFeat -o /tmp/case-on . && EXPECT_FEAT=on /tmp/case-on
```
