---
id: l16_bytecode_vm
dim: L16_template_interp
priority: P2
sources:
  - yaegi@v0.16.1: interp/compile_test.go:12 parser.ParseFile 后逐 ast.GenDecl/FuncDecl/Block/Expr 编译
  - yaegi@v0.16.1: interp/interp_eval_test.go:40 算术/比较/一元表达式求值与除零错误
  - yaegi@v0.16.1: interp/interp_eval_test.go:672 实参个数、类型与多返回值调用检查
  - gitea@v1.23.5: modules/templates/eval/eval_test.go:21 shunting-yard 表达式求值器（含函数调用）
  - gitea@v1.23.5: modules/templates/eval/eval_test.go:81 缺操作数/未知运算符等错误分类
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:1622 递归模板最大执行深度
tags: [L16, goast, bytecode, stack-vm, go-parser, control-flow, recursion, step-limit, GC, TORTURE_ROUNDS]
requires: [goast]
runtime: "<30s（TORTURE_ROUNDS 放大）"
---

# 场景

把纯整数函数源码编译为栈式字节码：`push/load/add/sub/mul/div/rem/neg`、比较、`jump/jz`、`call/ret`。支持 `if/return` 控制流、用户函数互调与递归（`fib`），在 VM 中执行并与手写 Go 参考实现交叉校验；同时检查未知函数/错误实参个数/不支持的 `for`/字符串字面量/缺少末尾 return/重复函数等编译错误，除零、递归深度、步数上限等运行时错误，以及字节码反汇编确定性、函数位置映射和跨 GC 重放。对应 yaegi 的编译/求值测试与 gitea 的表达式求值器。

# 期望行为

1. 合法源码编译出 8 个函数，函数行号有效、每个函数至少 2 条指令；
2. 两次编译的完整反汇编逐字节一致（编译确定）；
3. `fib(0..14)`、`poly`、`clamp`、`abs`、`nested`、`div`、`rem` 在随机参数下与 Go 参考实现一致（每轮 40 组）；
4. `div/rem` 除零、`inf` 递归深度、`nosuch` 未知函数、人为降低步数上限均返回含固定片段的错误；
5. 7 类非法源码在编译期报错，片段分别为 `unknown function "nope"`、`want 1 args, got 2`、`unsupported statement *ast.ForStmt`、`unsupported literal STRING`、`missing final return`、`unknown identifier "zzz"`、`duplicate function "f"`；
6. GC 后字节码、函数表、AST/位置映射不变，`fib(0..12)` 与固定样本重放结果一致；
7. `TORTURE_ROUNDS` 可放大轮数；最后一行 `ALL PASS l16_bytecode_vm`。

# 失败判读

- 参考结果不一致：字节码指令编码、跳转目标、栈顺序或整数语义错误；
- `bytecode compilation is not deterministic`：编译过程依赖 map 遍历顺序等不确定状态；
- 编译错误缺失/片段不符：AST 分派或参数校验退化；
- `division by zero`/`call depth`/`step limit` 未触发：VM 运行时保护失效；
- GC 后反汇编/位置/结果变化：程序对象或 AST 被破坏。

# 参数

- `TORTURE_ROUNDS`：默认 6 轮，可放大（上限 5000）；
- `UAF_SEED`：决定随机参数与轮数基准，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_bytecode_vm/*.go
TORTURE_ROUNDS=200 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=7 GODEBUG=clobberfree=1 /tmp/case
```
