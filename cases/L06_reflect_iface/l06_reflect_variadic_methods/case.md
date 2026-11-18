---
id: l06_reflect_variadic_methods
dim: L06_reflect_iface
priority: P1
sources:
  - gin@v1.10.0: routergroup_test.go:18 Group(relativePath, handlers ...HandlerFunc) 可变参数方法与链式调用
  - echo@v4.13.4: echo_test.go:1220 reflect.ValueOf(e).MethodByName("GET").Call(...) 反射批量注册
  - gorm@v1.30.0: schema/callbacks_test.go:22 反射方法集探测（值/指针接收者规则）
tags: [L06, reflect, variadic, Call, CallSlice, method-expression, method-set, promoted-method, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

定义指针接收者的 `Sum(prefix, ...*Item)`/`Add(delta, ...int64)` 与值接收者的
`Join(sep, ...string)`，经 `MethodByName/Call/CallSlice`、方法表达式、嵌入提升方法与接口动态派发
四条路径调用；覆盖零变参、nil 变参切片、`Call` 传切片元素与 `CallSlice` 传单个元素等错误路径。
方法值与变参切片保存在全局 reflect.Value 槽中，GC 后继续调用并校验 receiver/元素指针。

## 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_reflect_variadic_methods`；
2. `Type.Method` 的 Type 含 receiver（NumIn=3），`Value.Method` 不含（NumIn=2），两者 IsVariadic 均为 true；
3. `Call` 逐元素传变参、`CallSlice` 传切片、零变参与 nil 切片的结果全部正确；
4. 方法表达式首参为 receiver；嵌入 `*Calc` 提升后方法表达式 receiver 类型为外层类型；
5. `*Calc` 实现 summer/joiner，`Calc` 值只实现 joiner；嵌入值/指针的方法集符合 Go 规则；
6. 错误的参数个数、用切片冒充单个变参元素、CallSlice 传非切片均 panic 并被 recover；
7. GC 后方法值仍持有 receiver，变参切片中的 `*Item` 与 Tag 指针仍有效。

## 失败判读

- `MethodByName Call variadic`：变参打包或 receiver 绑定错误；
- `CallSlice ...`：调用约定把切片识别为单个参数，属 reflect 运行时 ABI 问题；
- `promoted variadic method ...`：嵌入方法集或 receiver 调整错误；
- `method value and variadic slice after GC`：方法值闭包/receiver 或变参切片指针未被扫描。

## 参数

- `UAF_SEED`：每轮基础值与变参个数来源；失败行带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_variadic_methods/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
