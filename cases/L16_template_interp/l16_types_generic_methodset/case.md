---
id: l16_types_generic_methodset
dim: L16_template_interp
priority: P1
sources:
  - yaegi@v0.16.1: interp/interp_eval_test.go:580 方法集：值/指针接收者、嵌入 struct、接口断言
  - hugo@v0.155.0: codegen/methods_test.go:27 反射/类型方法集枚举与签名生成
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:684 接口字段方法集与 nil 接收者调用（bug1/bug2）
  - golangci-lint@v2.0.2: pkg/golinters/unconvert/testdata/unconvert.go:30 用 types.Info 比较转换前后类型恒等
tags: [L16, goast, go-types, generics, instantiation, method-set, embedding, interface, constraints, GC]
requires: [goast]
runtime: "<10s"
---

# 场景

用 `go/parser` + `go/types` 检查含泛型声明的包：`Box[T]`、`Wrap[T]` 嵌入 `Box[T]`、约束接口 `Number`、泛型接口 `Adder[T Number]` 与具名类型 `MyInt`。对 `Box[int]`、`Wrap[string]`、`Adder[MyInt]`、`NumBox[int64]` 做 `types.Instantiate`，检查实例化后的值/指针方法集、提升方法、接收者/结果签名、`Implements` 与约束不满足错误。对应 yaegi 的方法集测试与 Hugo 的 codegen 方法枚举。

# 期望行为

1. `Box` 有一个类型参数 `T`，约束为 `any`；两次实例化 `Box[int]` 满足 `types.Identical`；
2. 方法集：`Box[int]` 值 `[Get]`、指针 `[Get Set]`；`Wrap[string]` 值 `[Get]`、指针 `[Get Set]`；`Adder[MyInt]` 为 `[Add Get]`；
3. `Get` 接收者为 `p.Box[int]`、结果 `int`；`Set` 接收者为 `*p.Box[int]`、参数 `int`；
4. `LookupFieldOrMethod(Wrap[string], true, "Get"/"Set")` 找到提升方法，索引长度为 2；
5. `MyInt` 与 `*MyInt` 都实现 `Adder[MyInt]`（2 个方法）；`Box[MyInt].Get` 结果类型为 `p.MyInt`；
6. `NumBox[string]` 实例化失败，错误含 `does not satisfy p.Number`，`NumBox[int64]` 成功；
7. GC 后所有实例化类型、方法集、签名与约束错误文本不变；
8. 最后一行 `ALL PASS l16_types_generic_methodset`。

# 失败判读

- `method set` 差异：值/指针接收者或嵌入提升规则错误；
- `not identical` / 类型串不符：实例化缓存或类型身份被破坏；
- `NumBox[string] unexpectedly instantiated`：约束校验失效；
- `does not satisfy` 错误文本变化：约束诊断路径异常；
- GC 后方法集/签名变化：Named/MethodSet 对象被回收或改写。

# 参数

- `UAF_SEED`：决定轮数（3–5），失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_types_generic_methodset/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 GODEBUG=clobberfree=1 /tmp/case
```
