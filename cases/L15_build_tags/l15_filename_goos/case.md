---
id: l15_filename_goos
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_filename_goos/where_linux.go:3 文件名后缀 _linux
  - cases/L15_build_tags/l15_filename_goos/arch_linux_amd64.go:3 双段后缀 _linux_amd64
  - cases/L15_build_tags/l15_filename_goos/arch_fallback.go:1 非 linux/amd64 回落约束
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, filename-suffix, GOOS, GOARCH, file-selection]
requires: [pkgbuild]
runtime: "<2s"
---

# 场景

`where_linux.go`/`where_windows.go`/`where_darwin.go` 不带任何 `//go:build`，完全依靠文件名 `_GOOS` 后缀选择；`arch_linux_amd64.go` 验证 `_GOOS_GOARCH` 双段后缀，其余平台由 `arch_fallback.go` 的 `!(linux && amd64)` 约束提供实现。

## 期望行为

1. `osSuffixImpl()` 等于当前 GOOS（linux/windows/darwin/other）；
2. `archSuffixImpl()=="linux-amd64"` 当且仅当 `GOOS==linux && GOARCH==amd64`，否则为 `"fallback"`；
3. GC 后选择结果与 payload 指针不变；
4. 退出码 0，最后一行 `ALL PASS l15_filename_goos`。

## 失败判读

- 后缀未生效（选中错误文件）：文件名后缀解析错误；
- 双段后缀在 linux/arm64 或 windows/amd64 上误选：GOOS+GOARCH 联合判定错误；
- 重复定义/符号缺失：回落约束与后缀隐含约束不互补。

## 参数

`BUILD_MODE=module`；可在任意 GOOS/GOARCH 上直接构建运行。

## 运行

```bash
cd cases/L15_build_tags/l15_filename_goos
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.24.1 go build -o /tmp/case-win .
```
