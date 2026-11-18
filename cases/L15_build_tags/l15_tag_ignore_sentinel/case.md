---
id: l15_tag_ignore_sentinel
dim: L15_build_tags
priority: P0
sources:
  - cases/L15_build_tags/l15_tag_ignore_sentinel/sentinel_ignore.go:1 ignore 作为普通 tag 名
  - cases/L15_build_tags/l15_tag_ignore_sentinel/sentinel_true.go:1 true 作为普通 tag 名
  - cases/L15_build_tags/l15_tag_ignore_sentinel/sentinel_false.go:1 false 作为普通 tag 名
  - cases/L15_build_tags/l15_tag_ignore_sentinel/negated.go:1 三者取反后参与编译
  - cases/L15_build_tags/l15_tag_ignore_sentinel/main.go:54 取反文件存在性校验
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, tag-vocabulary, negation, ignore, file-selection, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

构建约束中的 `ignore`、`true`、`false` 都是普通 tag 名，不是布尔字面量；
默认构建不设置这些 tag。三个哨兵文件因此被排除（它们同时含语法错误和重复
符号）；`//go:build !ignore && !true && !false` 的取反文件则必须参与编译。
若实现把 true/false 当布尔值，取反文件会被错误排除。

## 期望行为

1. 构建成功，三个哨兵文件均被排除；
2. `marker()=="base"`、`liveMarker=="base"`；
3. 取反文件提供的 `sentinelNeg=="neg-ok"` 存在；
4. init token 恰好为 `["base"]`；
5. GC 后上述状态不变；
6. 退出码 0，最后一行 `ALL PASS l15_tag_ignore_sentinel`。

## 失败判读

- `sentinelNeg is not declared`：取反表达式被判为假，`true`/`false` 被当成
  布尔字面量而非 tag；
- `redeclared` 或 token 数大于 1：`ignore/true/false` 哨兵文件未被排除；
- 语法错误直接暴露：排除时机错误；
- `marker` 不是 base：哨兵文件被选中。

## 参数

- 无额外参数；`BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_tag_ignore_sentinel
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
