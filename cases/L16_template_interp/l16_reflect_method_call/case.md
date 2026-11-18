---
id: l16_reflect_method_call
dim: L16_template_interp
priority: P1
sources:
  - yaegi@v0.16.1: interp/interp_eval_test.go:580 TestEvalMethod：值/指针方法集与接口断言
  - yaegi@v0.16.1: interp/interp_eval_test.go:672 TestEvalCall：实参个数/类型、变参与多返回值检查
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:684 bug1：*interface 上的方法调用
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:686 bug2：接口字段方法集为空
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:414 call 内建调用返回 error 的函数
tags: [L16, reflect, method-value, method-expression, embedding, interface-dispatch, variadic, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

用 reflect 枚举 `counter`/`*counter`/`wrapper`/`*wrapper` 的方法集（含嵌入 *counter 的提升方法），验证值类型不暴露指针接收者；通过 `Value.MethodByName`、`Value.Method(n)`、`Type.MethodByName(...).Func` 方法表达式、`Addr()`、接口动态值、`Call`/`CallSlice` 调用 Add/Value/Describe/SetTag/Variadic/Try；非法实参必须 panic。每轮 GC 后在绑定到同一接收者的方法值上再次调用，校验接收者字段、日志切片与类型方法集不变。

# 期望行为

1. 方法集名称/顺序：counter=[Value]、*counter=[Add Try Value Variadic]、wrapper=[Add Describe Try Value Variadic]、*wrapper=[Add Describe SetTag Try Value Variadic]，值 wrapper 不暴露 SetTag；
2. Add 通过 `Elem().Addr()`、promoted 值调用、接口动态调用依次得到 15+r、17+r、20+r，接收者 N 同步变化；
3. 方法表达式 `SetTag` 首参为 *wrapper，`Value` 首参为 wrapper；调用后 Tag/值正确；
4. Variadic 的 Call 得 `p:3`、CallSlice 得 `q:12`；Try(true) 返回 (N,nil)，Try(false) 的 error 为 `failed`；
5. 错误实参/缺失实参 panic 且不修改接收者；`reflect.ValueOf(iface)` 动态类型方法数为 4，静态接口类型方法数为 2；
6. GC 后方法值仍绑定原接收者（N=21+r），日志 `init/add:5/add:2/add:3/add:1` 完整；
7. 最后一行 `ALL PASS l16_reflect_method_call`。

# 失败判读

- `methods=... want ...`：方法集枚举（排序/提升/指针接收者）异常；
- `Add returned/ receiver N`：反射调用未落到真实接收者或参数传递错误；
- `method expr` / `CallSlice`：方法表达式签名或变参打包错误；
- `did not panic`：非法调用被静默接受；
- GC 后接收者/日志错乱或崩溃：方法值捕获的接收者指针被破坏；
- 超时：反射调用死循环（不应发生）。

# 参数

- `UAF_SEED`：决定轮数（6–9）与方法枚举顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_reflect_method_call/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GODEBUG=efence=1 /tmp/case
```
