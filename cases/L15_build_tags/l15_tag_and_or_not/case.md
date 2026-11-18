---
id: l15_tag_and_or_not
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_tag_and_or_not/and_true.go:1 AND 约束 linux && amd64
  - cases/L15_build_tags/l15_tag_and_or_not/or_true.go:1 OR 约束 linux || windows
  - cases/L15_build_tags/l15_tag_and_or_not/not_true.go:1 NOT 约束 !windows
  - cases/L15_build_tags/l15_tag_and_or_not/combo_on.go:1 自定义 tag 括号组合
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, AND, OR, NOT, GOOS, GOARCH, custom-tags]
requires: [pkgbuild]
runtime: "<2s"
---

# 场景

`andImpl`/`orImpl`/`notImpl` 分别由 `linux && amd64`、`linux || windows`、`!windows` 三条约束选择实现；期望值由运行时 `GOOS/GOARCH` 推导，因此任意平台结果确定。`comboImpl` 由自定义 tag 的 `tagX && (tagY || !tagY)` 与互补的 `!tagX` 选择，默认构建走 off 分支。

## 期望行为

1. `andImpl()=="and"` 当且仅当 `GOOS==linux && GOARCH==amd64`；
2. `orImpl()=="or"` 当且仅当 `GOOS==linux || GOOS==windows`；
3. `notImpl()=="not"` 当且仅当 `GOOS!=windows`；
4. 默认构建 `comboImpl()=="off"`；`-tags=tagX`（无论 tagY 是否设置）构建后为 `"on"`；
5. GC 后选择结果与 payload 指针不变；
6. 退出码 0，最后一行 `ALL PASS l15_tag_and_or_not`。

## 失败判读

- 实现选择与平台不匹配：`//go:build` 运算符优先级/括号解析错误；
- 默认构建出现 `"on"` 或带 tag 构建仍为 `"off"`：自定义 tag 或组合表达式解析错误；
- 构建报重复定义/符号缺失：互补约束集合不完整。

## 参数

- `EXPECT_TAGS=on`：仅在 `-tags=tagX` 构建时使用，默认 off；
- `BUILD_MODE=module`（目录构建才会应用构建约束）。

## 运行

```bash
cd cases/L15_build_tags/l15_tag_and_or_not
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOTOOLCHAIN=go1.24.1 go build -tags=tagX -o /tmp/case-x . && EXPECT_TAGS=on /tmp/case-x
```
