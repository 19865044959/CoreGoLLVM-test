---
id: l16_interp_closures
dim: L16_template_interp
priority: P2
sources:
  - yaegi@v0.16.1: example/closure/closure_test.go:10 解释函数返回闭包，闭包再返回闭包并跨宿主调用
  - yaegi@v0.16.1: interp/interp_eval_test.go:227 函数字面量求值为闭包、命名返回值与裸 return
  - yaegi@v0.16.1: interp/interp_eval_test.go:672 调用前实参个数检查与错误路径
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:1622 递归执行深度上限
tags: [L16, goast, interpreter, closures, environment-capture, higher-order, step-limit, depth-limit, GC, TORTURE_ROUNDS]
requires: [goast]
runtime: "<30s（TORTURE_ROUNDS 放大）"
---

# 场景

用 `go/parser` 解析自包含源码，实现支持闭包的最小树遍历解释器：函数字面量捕获定义处环境，参数绑定在调用环境，`=` 按作用域链更新捕获变量，支持高阶函数、返回函数值、函数组合、递归、if/for、整数/布尔运算，并设步数与调用深度上限。解释结果与 Go 编译执行参考实现交叉校验，对应 yaegi 的闭包/函数字面量/调用检查与 Hugo 的最大递归深度。

# 期望行为

1. 函数位置表精确（makeAdder 3、counter 15、sumTo 27、fib 34、choose 41、twice 48、spin 60、loop 66），16 个顶层函数全部入全局环境；
2. `makeAdder(7)(5)=12`，`apply(makeAdder(10),32)=42`，`compose(inc,dbl)(5)=11`；
3. 两个 `counter` 实例状态独立：c1 依次 3/4/0 → 3/7/7，c2 依次 1/-5 → 101/96，交错调用后 c1 仍为 7；
4. `choose(0,inc,dbl)(5)=6`、`choose(1,...)(5)=10`；`twice(inc,5)=7`、`twice(makeAdder(3),5)=11`；
5. `sumTo(10)=55`、`fib(10)=55`，与编译期参考实现一致；
6. 错误路径：调用非函数报 `not a function`，实参 0/2 个报 `expects 1 args`，`zzz` 报 `unknown identifier`，除零报 `division by zero`，`spin` 报 `step limit`，`loop` 报 `depth limit`，未知函数名报 `unknown function`；
7. GC 后 c1/c2 的捕获状态、`fib(12)` 结果与所有函数行号不变，AST 仍为同一对象；
8. `TORTURE_ROUNDS`（默认 24）轮随机顺序重放 sumTo/fib/twice/counter，每 4 轮 GC；最终 c1 状态与参考闭包一致；末行输出 `ALL PASS l16_interp_closures`。

# 失败判读

- `FAIL: ... got kind/i=...`：表达式/调用求值语义错误；
- `FAIL: c1/c2 ...`：闭包环境捕获或可变单元（cell）更新错误；
- `FAIL: line of ...`：函数位置表在 GC 后损坏；
- `FAIL: ... err=...`：限制器（步数/深度）或错误分类不符合预期；
- 死循环/超时：步数检查未生效；
- GC 轮次失败：闭包环境或 AST 被错误回收。

# 参数

- `TORTURE_ROUNDS`：默认 24，放大做长压测（上限 20000）；
- `UAF_SEED`：决定每轮执行顺序与 n 值，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_interp_closures/*.go
TORTURE_ROUNDS=200 UAF_SEED=6 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
