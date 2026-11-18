---
id: l15_buildtag_os
dim: L15_build_tags
priority: P1
sources:
  - docs/DIMENSIONS.md: L15 构建标签与多平台
tags: [L15, build-tag, GOOS, file-selection]
requires: [pkgbuild]
runtime: "<2s"
---

# 场景

同一包内两个文件分别带 `//go:build linux` 与 `//go:build !linux`，实现同一函数；按当前 GOOS 只能选中一个。用于验证条件编译、文件选择与链接。

# 期望行为

1. Linux 上 `platformImpl()=="linux-file"`，其他系统为 `"other-file"`；
2. GC 后结果不变；
3. 最后一行 `ALL PASS l15_buildtag_os`。

# 失败判读

- 选错实现：构建标签解析/文件选择错误；
- 重复定义/缺失符号：约束组合或链接错误。

# 参数

`BUILD_MODE=module`（目录构建才会应用 build tag）。

# 运行

```bash
cd cases/L15_build_tags/l15_buildtag_os
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
