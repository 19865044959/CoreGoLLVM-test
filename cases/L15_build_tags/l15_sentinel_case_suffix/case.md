---
id: l15_sentinel_case_suffix
dim: L15_build_tags
priority: P0
sources:
  - cases/L15_build_tags/l15_sentinel_case_suffix/base.go:5 正常实现
  - cases/L15_build_tags/l15_sentinel_case_suffix/bomb_ignore.go:1 大写 Ignore 哨兵
  - cases/L15_build_tags/l15_sentinel_case_suffix/bomb_true.go:1 大写 TRUE 哨兵
  - cases/L15_build_tags/l15_sentinel_case_suffix/bomb_false.go:1 大写 False 哨兵
  - cases/L15_build_tags/l15_sentinel_case_suffix/bomb_linux_upper.go:1 大写 Linux 哨兵
  - cases/L15_build_tags/l15_sentinel_case_suffix/neg_case.go:1 四个哨兵取反
  - cases/L15_build_tags/l15_sentinel_case_suffix/name_ignore.go:6 文件名含 ignore 照常编译
  - cases/L15_build_tags/l15_sentinel_case_suffix/main.go:51 token 集合校验
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, tag-vocabulary, case-sensitivity, ignore, filename-suffix, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

构建约束的大小写敏感：`Ignore`、`TRUE`、`False`、`Linux` 都是未设置的普通 tag 名，
不能折叠成 `ignore`/布尔字面量/内建 GOOS。四个哨兵文件（含语法错误与重复符号）
必须被排除，而 `!Ignore && !TRUE && !False && !Linux` 的取反文件必须参与编译。
同时，文件名里的 `ignore`/`true`/`false` 不是特殊后缀，相关文件照常编译。

## 期望行为

1. 构建成功，四个大写哨兵文件均被排除；
2. `marker()=="base"`、`negCase=="neg-ok"`；
3. `ignoreNameOK/trueNameOK/falseNameOK` 三个常量存在；
4. init token 恰为 `{base,name-false,name-ignore,name-true,neg-case}`（排序后比较）；
5. 两次 GC 后状态与 token 集合不变；
6. 退出码 0，最后一行 `ALL PASS l15_sentinel_case_suffix`。

## 失败判读

- `redeclared` 或语法错误暴露：大写哨兵被大小写折叠成已设置的 tag；
- `negCase` 未定义：取反表达式被判为假（大写哨兵或 Linux 被当成已设置）；
- 缺少 `name-*` token：文件名中的哨兵词被误认为特殊后缀；
- token 数大于 5：哨兵文件被编译；
- 这类失败优先判为 tag 词法/文件选择 bug。

## 参数

- 无额外参数；`BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_sentinel_case_suffix
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
