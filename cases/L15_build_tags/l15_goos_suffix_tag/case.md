---
id: l15_goos_suffix_tag
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_goos_suffix_tag/where_linux.go:1 _linux 后缀 + tagFast 交集
  - cases/L15_build_tags/l15_goos_suffix_tag/where_linux_base.go:1 linux && !tagFast
  - cases/L15_build_tags/l15_goos_suffix_tag/where_other.go:1 非 linux 兜底
  - cases/L15_build_tags/l15_goos_suffix_tag/main.go:49 GOOS 后缀选择校验
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, filename-suffix, GOOS, build-tag, conjunction, custom-tags, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

`where_linux.go` 同时具备文件名 `_linux` 后缀与 `//go:build tagFast` 约束，因此只有 linux + tagFast 才编译；默认 linux 构建必须排除它并选择 `where_linux_base.go`，非 linux 选择 `where_other.go`。验证文件名后缀与 tag 约束是 AND 关系。

## 期望行为

1. 默认 linux 构建 `suffixImpl()=="linux-slow"`，非 linux 为 `"other"`；
2. `-tags=tagFast` 的 linux 构建为 `"linux-fast"`；
3. 构建成功，证明后缀约束与 tag 约束不会互相覆盖而导致重复定义/符号缺失；
4. GC 后选择结果与 payload 不变；
5. 退出码 0，最后一行 `ALL PASS l15_goos_suffix_tag`。

## 失败判读

- 默认 linux 构建选中 linux-fast：文件名后缀被 tag 忽略（AND 未生效；通常会重复定义）；
- tagFast 构建仍为 linux-slow：tag 未参与后缀文件的约束；
- 非 linux 符号缺失：兜底约束错误。

## 参数

- `EXPECT_FAST=1`：仅在 linux + `-tags=tagFast` 构建时使用；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_goos_suffix_tag
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOTOOLCHAIN=go1.24.1 go build -tags=tagFast -o /tmp/case-fast . && EXPECT_FAST=1 /tmp/case-fast
```
