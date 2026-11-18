---
id: l06_method_expr_reflect
dim: L06_reflect_iface
priority: P1
sources:
  - gorm@v1.30.0: schema/callbacks_test.go:22 反射扫描类型方法集探测生命周期回调
  - echo@v4.13.4: echo_test.go:1220 reflect.ValueOf(e).MethodByName("GET").Call(...) 批量注册
  - hugo@v0.155.0: codegen/methods_test.go:27 reflect.Type.Method 遍历接口方法集与签名
  - caddy@v2.10.0: replacer_test.go:347 reflect.ValueOf(fn).Pointer() 校验函数身份
tags: [L06, reflect, method-expression, Method.Func, Value.Method, method-set, promotion, unexported-method, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

框架的反射调度层会遍历 `reflect.Type.Method(i)` 把方法当函数值调用（方法表达式），也会用
`Value.Method/ MethodByName` 取绑定方法。本用例用值接收者/指针接收者/匿名嵌入构造方法集，
校验方法排序、`Method.Index`、`Method.Func` 的接收者首参、`Value.Method` 的绑定形式、
提升方法与未导出方法的可见性，并把方法表达式/绑定方法保存到全局后跨 GC 继续调用。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_method_expr_reflect`；
2. 值类型方法集为 `BaseVal, Val`，指针类型为 `BasePtr, BaseVal, Ptr, Val`，按名字排序，
   `Method.Index` 与下标一致，未导出方法不出现；
3. `Method.Func.Type().In(0)` 对值类型是 `obj`、对指针类型是 `*obj`；
   `Method.Type` 同样把接收者作为首参；
4. `Method.Func.Call([receiver,arg])` 与 `Value.MethodByName(...).Call([arg])` 结果一致，
   指针方法通过两种路径都修改同一接收者；
5. 可寻址值不会自动获得指针方法，`Elem().Addr().MethodByName("Ptr")` 才可见；
6. 提升方法的接收者类型为外层类型；接口动态类型 `TypeOf` 为具体 `*obj`，接口派发结果一致；
7. 方法表达式与绑定方法跨 GC 后仍可调用，接收者字段完整。

## 失败判读

- `method count` / `method order`：方法集或排序计算错误；
- `method expression receiver type`：`Method.Func` 的接收者签名错误；
- `method expression result ...` / `bound method result ...`：调用约定或接收者传递错误；
- `addressable value keeps value method set`：可寻址性被错误用于扩展方法集；
- `... after GC`：方法表达式/绑定方法捕获的 receiver 未被扫描。

## 参数

- `UAF_SEED`：接收者初始计数值来源；失败输出带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_method_expr_reflect/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
