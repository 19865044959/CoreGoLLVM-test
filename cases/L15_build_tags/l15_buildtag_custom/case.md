---
id: l15_buildtag_custom
dim: L15_build_tags
priority: P1
sources:
  - docs/DIMENSIONS.md: L15 构建标签与多平台
tags: [L15, build-tag, custom-tags]
requires: [pkgbuild]
runtime: "<2s"
---

# 场景

自定义构建标签 `featureA`：默认构建选择 `!featureA` 实现（`featureImpl()=="off"`）；手动 `-tags=featureA` 构建应选择 `"on"`。判定期望由 `EXPECT_TAG` 指定。

# 期望行为

1. 默认运行（无 tags）`featureImpl()=="off"`；
2. 手动路径：`go build -tags=featureA` 后 `EXPECT_TAG=on` 运行通过；
3. GC 后结果不变；
4. 最后一行 `ALL PASS l15_buildtag_custom`。

# 失败判读

- 默认分支误选 `on`：构建标签解析错误；
- 带 tags 构建后仍为 `off`：`-tags` 未生效。

# 参数

`EXPECT_TAG=off|on`（默认 off）；`BUILD_MODE=module`；runner 可用 `BUILD_FLAGS='-tags=featureA' EXPECT_TAG=on` 覆盖。

# 运行

```bash
cd cases/L15_build_tags/l15_buildtag_custom
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOTOOLCHAIN=go1.24.1 go build -tags=featureA -o /tmp/case-a . && EXPECT_TAG=on /tmp/case-a
```
