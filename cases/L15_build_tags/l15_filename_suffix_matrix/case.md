---
id: l15_filename_suffix_matrix
dim: L15_build_tags
priority: P0
sources:
  - cases/L15_build_tags/l15_filename_suffix_matrix/m_linux.go:1 _GOOS 后缀
  - cases/L15_build_tags/l15_filename_suffix_matrix/m_amd64.go:1 _GOARCH 后缀
  - cases/L15_build_tags/l15_filename_suffix_matrix/m_linux_amd64.go:1 _GOOS_GOARCH 双段后缀
  - cases/L15_build_tags/l15_filename_suffix_matrix/m_unix.go:1 unix 不是 GOOS，普通文件
  - cases/L15_build_tags/l15_filename_suffix_matrix/m_linux_custom.go:1 末段非 GOARCH，普通文件
  - cases/L15_build_tags/l15_filename_suffix_matrix/main.go:53 后缀矩阵期望集合
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, filename-suffix, GOOS, GOARCH, file-selection, unix-meta-tag, gc]
requires: [pkgbuild]
runtime: "<2s"
---

# 场景

文件名后缀矩阵：`_linux`/`_windows`/`_darwin`（GOOS）、`_amd64`/`_arm64`/`_386`
（GOARCH）、`_linux_amd64` 等双段（GOOS_GOARCH），以及易混淆的普通文件
`m_unix.go`、`m_linux_custom.go`、`m_linux_amd64_extra.go`、`m_foo.go`。每个文件
在 init 登记 token，main 按 GOOS/GOARCH 推导期望集合并精确比对，能同时发现
"漏选"与"多选"。

## 期望行为

1. 四个普通文件（unix/linux_custom/linux_amd64_extra/foo）在任何平台都被编译；
2. 只追加当前 GOOS、GOARCH 对应的单段 token；
3. 仅当 (GOOS,GOARCH) 属于 linux-amd64、linux-arm64、windows-amd64、darwin-arm64 时追加双段 token；
4. `m_unix.go` 在 linux 上被编译（文件名 unix 不是 GOOS 约束）；
5. token 排序后与期望集合完全一致、无重复；GC 后结果不变；
6. 退出码 0，最后一行 `ALL PASS l15_filename_suffix_matrix`。

## 失败判读

- 多出 pair/unix token：后缀识别过宽（把非 GOOS 段当约束或把普通文件当后缀）；
- 缺少单段 token：后缀匹配未生效；
- 构建报重复符号：某文件被两个后缀规则同时选中；
- 普通文件名下符号缺失：`_unix`/`_linux_custom` 被错误当作约束。

## 参数

`BUILD_MODE=module`；无自定义 tag；期望值由 `runtime.GOOS/GOARCH` 推导。

## 运行

```bash
cd cases/L15_build_tags/l15_filename_suffix_matrix
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
