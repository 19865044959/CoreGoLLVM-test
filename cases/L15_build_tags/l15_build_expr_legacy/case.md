---
id: l15_build_expr_legacy
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_build_expr_legacy/legacy_or_true.go:1 旧式空格=OR
  - cases/L15_build_tags/l15_build_expr_legacy/legacy_or_false.go:1 旧式多行=AND 补集
  - cases/L15_build_tags/l15_build_expr_legacy/legacy_combo_true.go:1 旧式逗号=AND
  - cases/L15_build_tags/l15_build_expr_legacy/modern_tight_true.go:1 无空格括号表达式
  - cases/L15_build_tags/l15_build_expr_legacy/modern_prec_true.go:1 && 优先于 ||
  - cases/L15_build_tags/l15_build_expr_legacy/modern_group_true.go:1 NOT 作用于括号分组
  - cases/L15_build_tags/l15_build_expr_legacy/main.go:56 表达式期望值推导
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, build-expression, legacy-plusbuild, AND, OR, NOT, precedence, parentheses, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

覆盖 `//go:build` 表达式语法的边角与旧式 `// +build` 行：无空格的括号表达式
`(linux)&&(amd64)`、`&&` 优先于 `||`、`!` 作用于括号分组 `!(linux && (!windows || amd64))`，以及旧式
约束的三种分隔语义（空格=OR、逗号=AND、多行=AND）。每对文件约束严格互补，
构建成功与运行期函数值共同验证解析器。

## 期望行为

1. linux：legacyOS=="linux"；linux/darwin：legacyOR=="unixish"；linux/amd64：
   legacyCombo=="linux-amd64"、modernTight=="yes"、modernPrec=="hit"、
   modernGroup=="grouped"；
2. 其他平台返回对应补集值：other/plain/other/no/miss/nogroup（windows 时 modernPrec=="hit"）；
3. GC 后所有选择结果与 payload 不变；
4. 退出码 0，最后一行 `ALL PASS l15_build_expr_legacy`。

## 失败判读

- 构建报重复定义：某对约束并非严格互补，表达式优先级/结合性错误；
- 旧式文件全部缺失：`// +build` 行格式（空行、位置）被解析器拒绝；
- modernTight 选错：括号与无空格 token 解析错误；
- modernPrec 选错：`&&`/`||` 优先级错误；
- modernGroup 选错：`!` 与括号分组/AND 的结合处理错误。

## 参数

`BUILD_MODE=module`；无自定义 tag。旧式 `// +build` 在 Go 1.24 仍受支持但已废弃，
目标工具链为 go1.24.1。

## 运行

```bash
cd cases/L15_build_tags/l15_build_expr_legacy
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
