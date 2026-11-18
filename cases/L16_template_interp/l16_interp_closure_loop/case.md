---
id: l16_interp_closure_loop
dim: L16_template_interp
priority: P2
sources:
  - yaegi@v0.16.1: example/closure/closure_test.go:10 TestFunctionCall 返回闭包并调用
  - yaegi@v0.16.1: interp/compile_test.go:14 parser.ParseFile 后解释执行
  - yaegi@v0.16.1: interp/interp_file_test.go:101 ParseComments 驱动的文件解释
  - yaegi@v0.16.1: interp/interp_consistent_test.go:22 closure9-12 在 Go 1.22 下的循环变量语义差异
tags: [L16, goast, interpreter, closures, loop-variable, recursion, environment-capture, TORTURE_ROUNDS, GC]
requires: [goast]
runtime: "<30s（TORTURE_ROUNDS 放大）"
---

# 场景

模拟 yaegi 在 AST 上解释闭包代码：函数字面量按引用捕获环境，`for` 循环变量遵循
Go 1.22 的每轮独立绑定（每轮闭包看到不同的 `i`），同时覆盖互递归（even/odd）、
通过 `var f func(...)` 实现的自递归、闭包可变捕获（counter）、高阶组合与立即调用。
解释结果与同源码的编译期参考实现逐值交叉校验；TORTURE_ROUNDS 下随机 n/顺序重跑并 GC。

## 期望行为

1. `loopCapture(n)` 每轮闭包捕获独立 `i`，结果等于编译期实现（n=3 → 12，n=5 → 1234）；
2. `fact/n` 通过 `var f` 自递归得到正确阶乘；`even/odd` 包级互递归结果与参考一致；
3. `combine(n)` 高阶闭包 `compose(a,b)(0)` 立即调用，结果与编译期 `refCombine` 一致；
4. `counter(0)` 返回的闭包三次调用得到 5/12/19，GC 后同一闭包继续调用得到 20；
5. 未定义名字、参数个数不匹配、非函数调用均返回可判读错误；
6. 每轮 TORTURE_ROUNDS 随机 n∈[1,6] 并按种子打乱 5 个函数检查顺序，结果全部与参考一致；
7. 每轮重新构造 counter 闭包、调用、保留并在 GC 后继续使用；
8. 每 3 轮执行显式 GC，GC 后重新解释 `fact` 仍成功（AST/环境/闭包跨 GC 有效）。

## 失败判读

- `loopCapture(n)=...`：解释器复用了循环变量环境（未实现 per-iteration 绑定）；
- `fact/even/odd ...`：全局函数解析或递归调用环境链错误；
- `counter ...`：闭包捕获环境未按引用共享，或捕获环境在 GC 后被提前回收；
- `arity/undefined/non-function`：错误路径缺失；
- `round ... post-GC eval err=`：AST/环境在 GC 后失效；
- 超时：递归/循环步数限制失效或环境链成环导致死循环。

## 参数

- `UAF_SEED`：每轮随机 n 与检查顺序的种子；失败输出带种子；
- `TORTURE_ROUNDS`：放大轮数（默认 12，上限 50000）；每轮重解释 5 个函数 + counter 闭包。

## 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_interp_closure_loop/*.go
UAF_SEED=1 TORTURE_ROUNDS=200 GODEBUG=clobberfree=1 /tmp/case
```
