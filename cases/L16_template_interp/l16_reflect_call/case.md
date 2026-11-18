---
id: l16_reflect_call
dim: L16_template_interp
priority: P1
sources:
  - yaegi@v0.16.1: interp/interp_eval_test.go:653 解释代码调用宿主函数通过 reflect.Value.Call
  - yaegi@v0.16.1: interp/interp_eval_test.go:672 实参不足/类型不符/变参/多返回值调用检查
  - yaegi@v0.16.1: example/getfunc/getfunc_test.go:11 取函数值后 reflect.Value.Call(nil)
tags: [L16, reflect, reflect-call, pointer-args, variadic, MakeFunc, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

用 `reflect.Value.Call/CallSlice/MethodByName/MakeFunc` 动态调用携带指针参数的函数：`mutate(*node,int64) *node`、`replace(**node,string)`、方法值 `(*node).Combine(*node)`、变参 `joinAll(string,...*node)`、值拷贝 `bumpVal(node) node`、多返回值 `maybeErr(bool) (*node,error)`，并验证错误实参数量/类型会 panic。每轮 GC 后重新调用同一方法值/函数并检查返回指针链。对应 yaegi 的解释代码→宿主函数反射调用路径。

# 期望行为

1. `Call` 传入 `*node` 后接收者被修改，返回 `*node` 的 `Next` 指回原节点；
2. `replace` 通过 `**node` 替换指针；`Combine` 方法值绑定接收者，GC 后调用结果正确；
3. 变参 `Call` 与 `CallSlice` 得到相同输出；`MakeFunc` 包装内部再次 `Call` 成功；
4. 值拷贝 `bumpVal` 不修改原节点；`maybeErr(false)` 返回 nil 指针与非 nil error；
5. 实参不足/类型错误/传值代替指针均触发 panic 并被 recover 捕获；
6. GC 后所有 `*node` 字段与 `Next` 链有效，最后一行 `ALL PASS l16_reflect_call`。

# 失败判读

- `FAIL: round N ... after GC`：反射调用返回/接收的堆指针被回收或链断裂；
- `FAIL: ... variadic Call/CallSlice`：`...*T` 打包或 `CallSlice` 使用错误；
- `FAIL: ... did not panic`：反射实参校验行为与 Go 1.24 不符；
- `FAIL: method value after GC`：方法值闭包内的接收者指针失效；
- panic 未被 recover：调用约定/指针间接化实现错误。

# 参数

- `UAF_SEED`：决定轮数（8–12）与变参实参顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_reflect_call/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
