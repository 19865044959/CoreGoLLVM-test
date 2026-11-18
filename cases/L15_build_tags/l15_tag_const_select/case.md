---
id: l15_tag_const_select
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_tag_const_select/const_base.go:1 默认 !tagMath 常量
  - cases/L15_build_tags/l15_tag_const_select/const_math.go:1 tagMath 常量
  - cases/L15_build_tags/l15_tag_const_select/main.go:20 tagBase 用作数组长度
  - cases/L15_build_tags/l15_tag_const_select/main.go:49 switch 使用标签常量
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, constant, compile-time-array, constant-expression, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

`tagBase`/`tagLabel`/`tagMask` 由构建标签选择：默认 `!tagMath` 为 2/"base"/7，`-tags=tagMath` 为 5/"math"/16。`tagBase` 同时用作全局数组的编译期长度与 switch case，因此错误的常量选择会造成编译失败、长度不符或分支错乱。

## 期望行为

1. 默认 tagBase=2、tagLabel="base"、tagMask=7；tagMath 构建为 5/"math"/16；
2. `len(table)==tagBase*4`（8 或 20），数组元素按 tagBase 计算；
3. `tagMask==tagBase*3+1` 的派生常量成立；
4. switch 在 tagBase 与 tagBase+1 上返回 `base`/`next`；
5. GC 后常量派生值与 payload 不变；
6. 退出码 0，最后一行 `ALL PASS l15_tag_const_select`。

## 失败判读

- 数组长度错误：编译期常量选择错误（可能直接编译失败）；
- 常量值/标签错误：标签文件选择或常量求值错误；
- switch 分支错误：常量传播/编译期求值错误；
- 派生常量不一致：常量表达式求值顺序错误。

## 参数

- `EXPECT_CONST=math`：仅在 `-tags=tagMath` 构建时使用，默认 base；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_tag_const_select
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOTOOLCHAIN=go1.24.1 go build -tags=tagMath -o /tmp/case-math . && EXPECT_CONST=math /tmp/case-math
```
