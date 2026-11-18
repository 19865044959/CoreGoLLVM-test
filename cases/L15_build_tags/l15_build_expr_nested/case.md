---
id: l15_build_expr_nested
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_build_expr_nested/expr_on.go:1 (tagA || tagB) && !tagC
  - cases/L15_build_tags/l15_build_expr_nested/taut.go:1 恒真括号/OR/NOT 表达式
  - cases/L15_build_tags/l15_build_expr_nested/impossible.go:1 对矛盾式取反
  - cases/L15_build_tags/l15_build_expr_nested/nest_on.go:1 深层嵌套括号表达式
  - cases/L15_build_tags/l15_build_expr_nested/dm_true.go:1 !(tagA && tagB)
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, AND, OR, NOT, parentheses, custom-tags, tautology, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

覆盖 `//go:build` 表达式解析的极端组合：嵌套括号 + AND/OR/NOT、恒真式 `(linux || !linux) && (!windows || windows)`、对矛盾式 OR 取反、De Morgan 风格的 `!(tagA && tagB)`、以及 `(tagA && (tagB || tagC)) || (tagD && !tagA)` 深层嵌套。默认无自定义 tag 时所有期望值确定。

## 期望行为

1. 默认构建 `exprImpl()=="off"`、`deMorganImpl()=="not-both"`、`nestImpl()=="simple"`；
2. 恒真式文件被编译，`tautImpl()=="taut"`；
3. 矛盾式取反文件被编译，`impossibleImpl()=="possible"`；
4. `-tags=tagA` 时 expr 为 on；`-tags=tagA,tagB,tagC,tagD` 时 expr 为 off、deMorgan 为 both、nest 为 complex；
5. GC 后表达式选择结果与 payload 不变；
6. 退出码 0，最后一行 `ALL PASS l15_build_expr_nested`。

## 失败判读

- 构建报重复定义/符号缺失：互补表达式不是严格取反；
- 恒真/矛盾文件缺失：括号或 NOT 优先级解析错误；
- 某 tag 组合选择错误：AND/OR/NOT 结合性错误；
- 期望值由 `EXPECT_*` 覆盖，便于手动验证其他 tag 组合。

## 参数

- `EXPECT_EXPR=on|off`、`EXPECT_DM=not-both|both`、`EXPECT_NEST=simple|complex`：默认值对应无自定义 tag 构建；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_build_expr_nested
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOTOOLCHAIN=go1.24.1 go build -tags=tagA -o /tmp/case-a . && EXPECT_EXPR=on /tmp/case-a
GOTOOLCHAIN=go1.24.1 go build -tags=tagA,tagB,tagC,tagD -o /tmp/case-all . && EXPECT_DM=both EXPECT_NEST=complex /tmp/case-all
```
