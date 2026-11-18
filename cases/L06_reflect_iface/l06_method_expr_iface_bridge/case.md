---
id: l06_method_expr_iface_bridge
dim: L06_reflect_iface
priority: P1
sources:
  - yaegi@v0.16.1: interp/interp_eval_test.go:580 解释执行中的接口方法集与嵌入派发
  - echo@v4.13.4: echo_test.go:1226 reflect.Value.MethodByName(...).Call 批量注册反射方法
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:404 typed nil 接口值上的方法调用
  - caddy@v2.10.0: modules/caddyhttp/responsewriter_test.go:47 嵌入接口 wrapper 的动态派发
tags: [L06, method-expression, method-value, interface, typed-nil, embedding, reflect, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

模板/解释器与路由框架把"方法"当作函数值传递：既有接口类型的方法表达式（`I.M`），也有从
接口值取出的绑定方法值，还有嵌入接口提升出的方法。本用例对同一接口方法集分别走方法表达式、
方法值、嵌入 wrapper、接口值反射 `MethodByName` 四条路径，并覆盖绑定接收者后重新赋值接口、
typed nil 接收者、nil 接口 panic、方法表达式存入 `any` 表/chan 后跨 GC 调用。

# 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_method_expr_iface_bridge`；
2. `Scorer.Name/Score`、`Extra.Extra` 与提升的 `Extra.Name` 方法表达式类型分别为
   `func(Scorer) string`、`func(Scorer,int64) int64`、`func(Extra) int64`、`func(Extra) string`，
   对不同动态实现者返回正确结果；
3. 接口方法值绑定原动态接收者：把接口变量改指向另一实现者后，旧方法值仍调用原接收者，且
   receiver 只由方法值保活时跨 GC 可调用；
4. typed nil 指针实现者经接口方法表达式/方法值按指针方法规则调用；
5. 从 nil 接口取方法值、用 nil 接口调用方法表达式均 panic 并被 recover；
6. 嵌入接口的 wrapper 通过提升方法满足接口，方法表达式/绑定方法结果一致；
7. 方法表达式与方法值存入 `map[string]any`、`chan any`、全局接口槽后跨 GC 仍可按原签名调用；
   接口值上的反射方法派发与直接调用一致，具体类型方法表达式类型不同于接口方法表达式。

# 失败判读

- `... expression type`：接口方法表达式的 receiver 参数或签名错误；
- `... pins original receiver` / `... after GC`：方法值闭包捕获了错误接收者，或接收者未被扫描；
- `typed nil ...`：typed nil 动态接收者的方法值/表达式派发错误；
- `... panics` 缺失：nil 接口方法值创建未触发 runtime panic；
- `reflect interface method dispatch`：接口 Value 的方法派发错误；
- `... from any table`：方法值装箱进接口后的函数指针或环境未被保活。

# 参数

- `UAF_SEED`：实现者的基值与标签值来源；失败行带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_method_expr_iface_bridge/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
