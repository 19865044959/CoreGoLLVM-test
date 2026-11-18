---
id: l15_suffix_same_partition
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_suffix_same_partition/v_linux.go:1 同一后缀下的 amd64 分支
  - cases/L15_build_tags/l15_suffix_same_partition/w_linux.go:1 同一后缀下的 arm64 分支
  - cases/L15_build_tags/l15_suffix_same_partition/x_linux.go:1 同一后缀下的其余 GOARCH
  - cases/L15_build_tags/l15_suffix_same_partition/y_other.go:1 非 linux 分支
  - cases/L15_build_tags/l15_suffix_same_partition/z_linux.go:1 后缀与 tag 矛盾
  - cases/L15_build_tags/l15_suffix_same_partition/q_linux_amd64.go:1 成对后缀与 tag 取反矛盾
  - cases/L15_build_tags/l15_suffix_same_partition/main.go:50 期望实现与唯一 token
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, filename-suffix, build-tag, GOOS, GOARCH, conjunction, mutual-exclusion, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

文件名后缀与 `//go:build` 约束是交集（AND）。三个文件都以 `_linux` 结尾，由 tag
再按 GOARCH 划分成互斥的 amd64 / arm64 / 其他分支；`y_other.go` 的 `_other` 不是
GOOS，只靠 `!linux` 约束。另有两个矛盾文件：`z_linux.go`（后缀 linux + 约束
windows）与 `q_linux_amd64.go`（成对后缀 linux_amd64 + 约束 !amd64），交集为空，
任何平台都不应编译。

## 期望行为

1. linux/amd64 选中 `v-linux-amd64`；linux/arm64 选中 `w-linux-arm64`；
2. linux 其余 GOARCH 选中 `x-linux-other`；非 linux 选中 `y-nonlinux`；
3. `impl()` 返回对应实现名；
4. init token 恰好 1 个且等于期望 token；
5. `z`/`q` 两个矛盾文件在任何平台都不编译（否则重复符号或 token 数大于 1）；
6. GC 后实现值与 token 不变；
7. 退出码 0，最后一行 `ALL PASS l15_suffix_same_partition`。

## 失败判读

- `redeclared` 或 token 数大于 1：后缀与 tag 被当成并集，或后缀过滤缺失；
- 同一后缀分支多了/少了：tag 对 GOARCH 的划分错误；
- 非 linux 走了 linux 分支：`_other` 被误认为特殊后缀，或 tag 选择错误；
- 这类失败优先定位到文件选择语义。

## 参数

- 无额外参数；`BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_suffix_same_partition
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOARCH=arm64 GOTOOLCHAIN=go1.24.1 go build -o /tmp/case.arm64 .
GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.24.1 go build -o /tmp/case.exe .
```
