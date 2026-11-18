---
id: l15_osarch_combo_tag
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_osarch_combo_tag/osarch_linux_arm64.go:1 linux && arm64 组合
  - cases/L15_build_tags/l15_osarch_combo_tag/osarch_windows_amd64.go:1 windows && amd64 组合
  - cases/L15_build_tags/l15_osarch_combo_tag/osarch_unix_386.go:1 (linux || freebsd) && 386 组合
  - cases/L15_build_tags/l15_osarch_combo_tag/osarch_other.go:1 组合取反兜底
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, GOOS, GOARCH, AND, OR, NOT, file-selection, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

`osArchImpl` 由五个文件的 `//go:build` 表达式按 GOOS/GOARCH 组合选择：`linux && arm64`、`windows && amd64`、`darwin && arm64`、`freebsd && amd64`、`(linux || freebsd) && 386`，其余平台由对全部组合取反的兜底文件提供。期望值完全由运行时 `runtime.GOOS/GOARCH` 推导。

## 期望行为

1. 当前平台对应的组合实现被选中（linux/arm64、windows/amd64、darwin/arm64、freebsd/amd64、linux|freebsd/386、other）；
2. 构建成功（约束两两互斥，无重复符号）；
3. GC 后选择结果与 payload 不变；
4. 退出码 0，最后一行 `ALL PASS l15_osarch_combo_tag`。

## 失败判读

- 实现与平台组合不匹配：`&&`/`||` 与括号在 GOOS/GOARCH 上的解析错误；
- 构建报重复定义：组合约束交叠；
- 符号缺失：兜底约束不是精确取反。

## 参数

`BUILD_MODE=module`；可在任意 GOOS/GOARCH 上直接构建运行，无需额外 tag。

## 运行

```bash
cd cases/L15_build_tags/l15_osarch_combo_tag
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
