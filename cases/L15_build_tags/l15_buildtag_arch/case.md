---
id: l15_buildtag_arch
dim: L15_build_tags
priority: P1
sources:
  - docs/DIMENSIONS.md: L15 构建标签与多平台
tags: [L15, filename-suffix, GOARCH, file-selection]
requires: [pkgbuild]
runtime: "<2s"
---

# 场景

`arch_amd64.go` / `arch_arm64.go` / `arch_other.go` 通过文件名后缀与 build 约束按 GOARCH 选择实现，验证跨平台文件选择与链接。

# 期望行为

1. 当前 GOARCH 与 `archImpl()` 返回值一致（amd64/arm64/other）；
2. GC 后结果不变；
3. 最后一行 `ALL PASS l15_buildtag_arch`。

# 失败判读

- 选中错误文件或符号缺失：文件后缀/构建约束解析问题；
- 多实现冲突：约束交叠。

# 参数

`BUILD_MODE=module`；可在 arm64 机器上直接跑同一用例。

# 运行

```bash
cd cases/L15_build_tags/l15_buildtag_arch
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
