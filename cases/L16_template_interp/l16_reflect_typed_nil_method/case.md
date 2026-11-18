---
id: l16_reflect_typed_nil_method
dim: L16_template_interp
priority: P1
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:342 Stringer/Error 接口值渲染
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:404 typed nil 接口仍可调用方法
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:1638 Execute 数据为 reflect.Value 时 index 结果可寻址并调方法
  - yaegi@v0.16.1: interp/interp_eval_test.go:653 解释代码调用宿主函数，统一为 reflect.Value
  - yaegi@v0.16.1: interp/interp_eval_test.go:732 解释出的接口类型对 reflect.TypeOf 的观测
tags: [L16, reflect, typed-nil, interface, method-value, method-expression, receiver-capture, panic, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

用反射检查 `Speaker` 接口（嵌入 `Named`）与 `*base`/`base` 的具体方法集，覆盖 typed nil 接口 `var s Speaker = (*base)(nil)`：`ValueOf(s)` 为 nil 指针、可调用带 nil guard 的指针方法，调用值接收者方法 panic；接口 Value 的静态方法集与动态 `Elem`。再验证方法表达式、绑定方法值捕获接收者（接口重新赋值后仍指向旧 nil 接收者）、真实接收者 `Bump` 的反射调用、错误参数/零 Value 的 panic，以及 GC 后的稳定性。

# 期望行为

1. 接口类型 `Speaker` 有 2 个方法 `Name,Speak`，`Method.Func` 无效、签名 `func() string`；`*base` 方法集 `Bump,Name,Speak`，`base` 仅 `Name`；
2. 方法表达式 `(*base).Speak` 用 nil 接收者调用返回 `nil-speak`，`base.Name` 用值调用返回 `z`；
3. typed nil 的 `ValueOf` 为 Ptr/IsNil、3 个方法；调用 `Speak` 得 `nil-speak`，调用 `Name` panic 且文本含 `nil *base pointer`；
4. 接口 Value 的 `NumMethod()==2`、`Elem()` 为 nil 指针、`MethodByName("Speak")` 得 `nil-speak`；
5. 接口重新赋值为 `*other` 后 `Speak` 为 `other`，旧绑定方法值仍返回 `nil-speak`；动态类型字符串为 `*main.other`；
6. `Bump` 方法值 +5、方法表达式 +3、GC 后 +2，接收者 `N` 依次为 15+r、18+r、20+r；
7. 错误参数 panic 含 `too many input arguments`；零 Value 的 `MethodByName`/`Call` panic 含 `zero Value`；panic 文本跨轮次/GC 稳定；
8. 最后一行 `ALL PASS l16_reflect_typed_nil_method`。

# 失败判读

- 方法集/签名不符：reflect 方法表或接口方法集错误；
- `nil Speak` 失败：typed nil 或接口动态类型信息丢失；
- `bound nil method after reassign`：方法值未捕获旧接收者；
- `Bump after GC` 非预期：绑定接收者或对象指针被破坏；
- panic 文本变化：反射 panic 包装路径异常。

# 参数

- `UAF_SEED`：决定轮数（5–8），失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_reflect_typed_nil_method/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=5 GODEBUG=clobberfree=1 /tmp/case
```
