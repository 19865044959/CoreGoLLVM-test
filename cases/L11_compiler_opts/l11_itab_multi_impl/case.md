---
id: l11_itab_multi_impl
dim: L11_compiler_opts
priority: P1
sources:
  - echo@v4.13.4: binder_test.go:2102 TestValueBinder_BindUnmarshaler 多种 Unmarshaler 实现的接口分派
  - gorm@v1.30.0: schema/callbacks_test.go:22 TestCallback 反射探测多个生命周期回调方法集
  - goccy-json@v0.10.5: internal/encoder/encode_opcode_test.go:8 TestDumpOpcode 按类型/opcode 分派编码路径
tags: [devirtualize, interface-dispatch, itab, type-switch, method-value, generics, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 Binder/ORM 回调/编码器 opcode 中“同一接口多实现”的分派：`codec` 接口有 12 种具体实现（10 个嵌入公共基类型的值类型、1 个指针接收者类型、1 个异或公式的值类型）。对每项分别执行直接具体调用、接口调用、参考公式、type switch 与泛型静态调用；再构造 120 次混合接口切片分派、方法值切片、类型断言成功/失败与接口相等性比较。所有接口值与方法值在显式 GC 后重新分派。

# 期望行为

1. 12 种实现的 `Encode`/`Kind` 经接口调用与参考公式一致；
2. 每种实现直接调用与接口调用结果相同（去虚拟化等价）；
3. 泛型 `encodeStatic` 对值/指针实现与参考公式一致；
4. 120 次混合分派总和等于逐项参考和；
5. 方法值在 GC 后调用总和与 kind 均正确；
6. type switch 命中全部 12 种动态类型，未知类型返回 `?`；
7. 正确类型断言成功、错误类型断言 comma-ok 返回 false、指针实现断言取到字段；
8. 相同动态类型/值的接口相等，不同动态类型不等；
9. 最后一行输出 `ALL PASS l11_itab_multi_impl`。

# 失败判读

- `interface Encode/Kind dispatch`：itab 方法槽位错误；
- `direct vs iface` / `generic static call`：去虚拟化/静态调用结果不一致；
- `mixed interface dispatch sum`：多实现分派到错误方法；
- `method value ... after GC`：方法值捕获的接收者失效；
- `type switch kind` / `type assertion`：接口类型元数据错误；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定测试输入 `x`（-18..18），失败输出带种子；
- 无 `TORTURE_ROUNDS`（P1，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_itab_multi_impl/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_itab_multi_impl/*.go
UAF_SEED=1 /tmp/case_n
```
