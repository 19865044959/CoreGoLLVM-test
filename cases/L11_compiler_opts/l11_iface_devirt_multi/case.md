---
id: l11_iface_devirt_multi
dim: L11_compiler_opts
priority: P1
sources:
  - echo@v4.13.4: binder_test.go:2102 TestValueBinder_BindUnmarshaler 多种 Unmarshaler 实现的接口分派
  - echo@v4.13.4: echo_test.go:1220 reflect.ValueOf(e).MethodByName("GET").Call(...) 与直接调用对照
  - gorm@v1.30.0: schema/callbacks_test.go:22 TestCallback 反射探测多个生命周期回调方法集
tags: [devirtualize, interface-dispatch, type-switch, method-value, generics, typed-nil, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟绑定器与回调注册表的多实现分派：接口含 `ID/Calc/Tag`，由 10 个嵌入基类的值实现、2 个指针实现、1 个 nil 安全指针实现共同满足。对每个实现分别走接口动态分派、方法值、`type switch` 具体类型分支直接调用、局部静态已知类型的接口调用、泛型约束调用与方法表达式；另测接口相等性、窄接口/接口到接口断言、`ider`-only 类型断言失败、nil 接口调用与 typed nil 解引用 panic。实现切片跨 3 轮 GC 后复核。

# 期望行为

1. 13 个实现的 `ID`、`Tag` 与规格表一致；
2. 每个实现在所有测试输入下，接口调用、方法值、`type switch` 直接调用的结果等于独立公式；
3. nil 安全指针实现的 `Calc` 经接口返回 `-1`，`ID/Tag` 正常；
4. 方法表达式 `opA.Calc`、`(*opM).Calc` 与直接调用一致；
5. 泛型 `genericCalc[T op]` 对值/指针实现给出公式结果；
6. 局部静态类型已知的接口调用结果与公式一致；
7. 相同动态类型/值的接口相等，不同动态类型不等；
8. `ider`-only 值断言 `op` 失败，接口到接口断言成功；
9. nil 接口调用与 typed nil 指针方法解引用均 panic；typed nil 接口 `!= nil`；
10. 全部接口值在最终 GC 后 `ID/Tag/Calc` 不变；
11. 最后一行输出 `ALL PASS l11_iface_devirt_multi`。

# 失败判读

- `interface dispatch mismatch` / `method value dispatch mismatch`：去虚拟化或方法值绑定改变结果；
- `type-switch direct dispatch mismatch` / `type switch missed concrete type`：具体类型分支缺失或错派；
- `interface equality`：接口相等性比较错误；
- `nil-safe` / `typed-nil`：nil 接收者或 typed nil 语义错误；
- `retained interface ... after GC`：接口数据/itab 被回收或破坏；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定附加的调用输入与接口值存活规模，失败输出带种子；
- 本用例为 P1，不支持 `TORTURE_ROUNDS`。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_iface_devirt_multi/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_iface_devirt_multi/*.go
UAF_SEED=1 /tmp/case_n
```
