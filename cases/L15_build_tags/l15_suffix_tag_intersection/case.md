---
id: l15_suffix_tag_intersection
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_suffix_tag_intersection/impl_linux.go:1 后缀与 tag 同时为 linux
  - cases/L15_build_tags/l15_suffix_tag_intersection/impl_windows.go:1 后缀 windows 与 tag linux 矛盾
  - cases/L15_build_tags/l15_suffix_tag_intersection/impl_arm64.go:1 后缀 arm64 与 tag amd64 矛盾
  - cases/L15_build_tags/l15_suffix_tag_intersection/dual_linux_amd64.go:1 成对后缀与同义约束
  - cases/L15_build_tags/l15_suffix_tag_intersection/main.go:64 排除文件的 init 计数
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, filename-suffix, build-tag, GOOS, GOARCH, conjunction, mutual-exclusion, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

文件名后缀与 `//go:build` 约束是交集关系：两者都成立才编译。`impl_linux.go`
同时要求后缀 linux 与约束 linux；`impl_windows.go` 的后缀 windows 与约束 linux
矛盾，`impl_arm64.go` 的后缀 arm64 与约束 amd64 矛盾，任何平台都不应编译它们。
若实现误把两者当并集，这两个文件会与正常实现产生重复符号。

## 期望行为

1. linux 下 `impl()=="linux"`，其他 GOOS 下 `impl()=="other"`；
2. linux/amd64 下 `dualArc()=="suffix+tag:linux-amd64"`，其他平台为 `fallback`；
3. 平台 init token 恰好 1 个，等于被选中的实现；
4. 两个矛盾文件既不参与编译也不执行 init；
5. GC 后实现值与 token 集合不变；
6. 退出码 0，最后一行 `ALL PASS l15_suffix_tag_intersection`。

## 失败判读

- `redeclared in this block` 或 init token 数大于 1：矛盾文件被编译，后缀与
  tag 被当成并集，或后缀过滤未参与文件选择；
- `dualArc` 在非 linux/amd64 平台返回 linux 分支：成对后缀匹配过宽；
- 平台实现错误：后缀与约束的 AND 语义或 GOOS 选择错误；
- GC 后 token 变化：init 注册表被破坏。

## 参数

- 无额外参数；`BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_suffix_tag_intersection
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOOS=linux GOARCH=arm64 GOTOOLCHAIN=go1.24.1 go build -o /tmp/case.arm64 .
```
