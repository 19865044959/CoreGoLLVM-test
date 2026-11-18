---
id: l06_iface_interp_eval
dim: L06_reflect_iface
priority: P2
sources:
  - yaegi@v0.16.1: interp/interp_eval_test.go:580 解释执行中的方法集、嵌入与接口断言
  - yaegi@v0.16.1: interp/interp_eval_test.go:653 解释代码经 reflect.Value.Call 调用宿主 func
  - yaegi@v0.16.1: interp/interp_eval_test.go:732 解释出的接口类型被 reflect.TypeOf 观测
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:684 *interface 与接口字段上的方法调用
tags: [L06, reflect, interface-dispatch, interpreter, type-switch, reflect-call, interface-assertion, closure, GC, torture]
requires: [reflect]
runtime: "<60s"
---

# 场景

模板引擎/脚本解释器把 AST 节点与宿主能力都表示为接口（`Expr`/`Op`），同一表达式可能经
直接接口派发、具体类型 switch 的解释路径、以及 `reflect.Value.Call` 入口执行。本用例实现
一个带变量/加减乘/取负/宿主回调的迷你解释器，对每个表达式同时走三条路径，校验结果一致；
并把表达式、环境、接口实现与函数值闭包放入全局，跨 GC 重算。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_iface_interp_eval`；
2. `Expr`/`Op` 的反射方法集（导出方法数、排序、`Method.Func` 接口零值）正确；
3. 直接 `Eval`、类型 switch 解释器、反射 `MethodByName("Eval").Call` 三条路径对同一表达式
   返回相同结果，且无错误；
4. 宿主 `Op` 的 `Apply/Name` 经直接接口调用与 `reflect.Value.Call` 结果一致，接口方法值在
   GC 后仍可调用；
5. 未定义变量、未知 op、未知表达式类型在三条路径上都返回非 nil error；
6. `[]any`/接口断言/`reflect.ValueOf` 的装箱与解箱在 GC 后保持值不变；
7. 默认 40 轮在 60s 内完成，`TORTURE_ROUNDS` 可放大且结果仍确定。

## 失败判读

- `Expr interface shape` / `Op interface shape`：接口方法集或排序错误；
- `three eval paths agree`：某条解释路径的派发、变量读取或运算错误；
- `reflect op apply` / `reflect op name`：`reflect.Value.MethodByName` 调用约定错误；
- `boxed ... after GC`：接口装箱值或闭包捕获的指针未被扫描；
- `... error` 缺失：错误路径被吞掉或返回零值。

## 参数

- `UAF_SEED`：变量初值、op 参数与表达式常量来源；失败输出带出种子；
- `TORTURE_ROUNDS`：轮数（默认 40，上限 2000）。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_iface_interp_eval/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=1000 UAF_SEED=7 /tmp/case
```
