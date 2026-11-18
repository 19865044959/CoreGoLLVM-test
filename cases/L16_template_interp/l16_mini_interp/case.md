---
id: l16_mini_interp
dim: L16_template_interp
priority: P2
sources:
  - yaegi@v0.16.1: interp/interp_eval_test.go:227 TestEvalFunc：函数调用与命名返回值
  - yaegi@v0.16.1: interp/interp_eval_test.go:672 TestEvalCall：实参个数、变参与多返回值
  - yaegi@v0.16.1: interp/interp_eval_test.go:87 TestOpVarConst：变量/常量与运算
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:1622 TestMaxExecDepth：递归深度上限
  - gitea@v1.23.5: modules/templates/eval/eval_test.go:21 表达式求值器与错误路径
tags: [L16, goast, interpreter, recursion, go-parser, step-limit, GC, TORTURE_ROUNDS]
requires: [goast]
runtime: "<30s（TORTURE_ROUNDS 放大）"
---

# 场景

用 `go/parser` 解析一段自包含 Go 源码，实现一个只支持 int64 的树遍历解释器：函数参数绑定、递归调用、`:=`/`=`、`++`、if/else、三种 for、比较与算术、调用深度和步数上限。对 fib/accum/gcd/mix 与编译执行参考实现逐值比对；div 除零、spin 空循环、未定义函数/变量、实参个数错误必须返回确定错误。每轮 GC 后复用 AST 与函数表重放，校验位置表与结果。

# 期望行为

1. fib(0..18)、随机 accum(0..40)、gcd(0..999)、mix(-20..20) 的解释结果与 Go 编译执行完全一致；
2. `div(21,4)=5`，`div(1,0)` 返回含 `division by zero` 的错误；
3. `spin(3)` 在步数上限处返回 `step limit exceeded (500000)`，不会挂死；
4. 调用未定义函数/变量分别返回 `unknown function "nope"`、`unknown identifier "zzz"`；实参个数错误返回 `expects 1 args`；
5. 8 个函数均被解析，位置映射（行号）在 GC 前后一致；
6. GC 后 fib(15)/accum(25) 及历史输入重放结果与参考实现一致；
7. 最后一行 `ALL PASS l16_mini_interp`。

# 失败判读

- `fib/accum/gcd/mix ... want`：解释器算术、循环或递归语义错误；
- `step limit`：空循环未受限，可能死锁/超时；
- `division by zero`：除零未转成错误（Go panic 未被拦截）；
- `unknown function/identifier`：符号解析错误；
- `position changed after GC`：AST 节点/位置表被破坏；
- 超时：步数上限失效或 `TORTURE_ROUNDS` 过大。

# 参数

- `TORTURE_ROUNDS`：默认 6 轮随机输入，可放大（上限 200000）；
- `UAF_SEED`：决定轮数与随机输入序列，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_mini_interp/*.go
TORTURE_ROUNDS=200 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=5 GODEBUG=efence=1 /tmp/case
```
