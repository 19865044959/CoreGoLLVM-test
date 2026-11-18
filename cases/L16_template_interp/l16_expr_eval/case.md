---
id: l16_expr_eval
dim: L16_template_interp
priority: P1
sources:
  - gitea@v1.23.5: modules/templates/eval/eval_test.go:21 表达式求值器 Expr(tokens...) 与类型化结果
  - gitea@v1.23.5: modules/templates/eval/eval_test.go:44 嵌套表达式/函数调用与 int-float 提升
  - yaegi@v0.16.1: interp/interp_eval_test.go:40 算术/比较/移位表达式求值与错误文本
tags: [L16, goast, go-parser, go-types, constant-folding, expression-eval, GC]
requires: [goast]
runtime: "<10s"
---

# 场景

实现一个只支持整型/布尔常量的最小求值器：`parser.ParseExpr` 得到 AST 后递归求值 `+ - * / % << >> & | ^ &^`、一元 `+ - ^ !`、比较与 `&&/||` 短路，使用 `go/constant` 的任意精度运算；同时把同一表达式嵌入 `const C = ...` 交给 `go/types.Config.Check` 独立折叠，逐条与自研求值器交叉比对。非法表达式（除零、类型不匹配、负移位、未知标识符、语法错误）必须被双方拒绝。

# 期望行为

1. 24 条合法表达式全部通过，例如 `1+2*3=7`、`7/2=3`、`-7/2=-3`、`1<<63=9223372036854775808`、`1<<64+1=18446744073709551617`、`^1=-2`、`'a'+1=98`、`1_000_000+24=1000024`、`7 &^ 3=4`、`true||(1==2)=true`；
2. 每条表达式的自研求值结果与 `go/types` 的常量值 `ExactString()` 完全一致；
3. 除零/负移位/`1+true`/`true&&1`/未知标识符返回确定错误；语法错误在 parse 阶段即失败，且 `go/types` 同样拒绝；
4. GC 后保存的 AST、`constant.Value` 与 `*types.Package` 仍可重新求值/查询 `C` 常量；
5. 最后一行 `ALL PASS l16_expr_eval`。

# 失败判读

- `FAIL: eval ... want ...`：求值器运算语义（截断除法、任意精度移位、按位补码、短路）与 Go 常量规则不符；
- `FAIL: evaluator/types disagree`：自研求值与 `go/types` 常量折叠结果不一致；
- `FAIL: types check ...`：表达式在 Go 常量语义下非法，普通量/溢出/类型规则有误；
- `FAIL: bad ...`：非法表达式未被拒绝或错误片段缺失；
- GC 后失败/panic：AST 或 `types.Package` 对象图被破坏。

# 参数

- `UAF_SEED`：决定轮数（3–5）、合法表达式顺序与非法用例顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_expr_eval/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
