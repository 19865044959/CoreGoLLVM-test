---
id: l11_devirt_iface_field
dim: L11_compiler_opts
priority: P1
sources:
  - echo@v4.13.4: binder_test.go:2102 TestValueBinder_BindUnmarshaler 多种 Unmarshaler 实现的接口分派
  - gorm@v1.30.0: schema/callbacks_test.go:22 TestCallback 反射探测生命周期回调方法集
  - grpc-go@v1.71.1: benchmark/primitives/primitives_test.go:449 接口调用与 :458 具体类型调用对照
tags: [devirtualize, interface-field, embedded-interface, interface-dispatch, type-assertion, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 Binder/ORM 回调注册中“接口字段承载实现”的分派：三种实现（值接收者、指针接收者、异或公式）混装在 struct 的接口字段、接口切片、map 与嵌入接口中。对每个实例分别走接口字段调用、struct 方法内字段调用、接口参数调用、嵌入提升调用、type switch/类型断言后的具体调用，并与参考结果比较；随后重新绑定字段、GC 后复核仅由接口字段保活的实现对象。nil 接口字段调用必须 panic。

# 期望行为

1. 36..55 个混合实现经接口字段、具体类型和接口参数调用的结果一致；
2. `Kind()` 经字段与直接调用一致；
3. 指针链与全部 pipe 标签在 GC 后完整；
4. 接口字段重新绑定后派发到新实现；
5. 嵌入接口的提升方法结果与直接调用一致；
6. map 与 slice 中的接口分派求和等于逐项参考；
7. 类型断言到三种具体类型后的调用结果一致；
8. nil 接口字段调用 panic 且文本含 `nil pointer dereference`；
9. 最终 GC 后 pipe 标签、接口字段和实现切片均非 nil；
10. 最后一行输出 `ALL PASS l11_devirt_iface_field`。

# 失败判读

- `interface field vs concrete dispatch`：接口字段去虚拟化后调用了错误实现或参数错误；
- `interface field rebinding`：字段重绑定未生效；
- `embedded interface promotion`：嵌入接口方法集/派发错误；
- `map/slice interface dispatch`：容器中接口值或 itab 错误；
- `type assertion to concrete dispatch`：断言后具体调用结果不一致；
- `nil interface field call panics`：nil 派发路径未 panic；
- `... after final GC`：仅由接口字段保活的对象被回收；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定实现数量 36..55 与输入基准值，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P1，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_devirt_iface_field/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_devirt_iface_field/*.go
UAF_SEED=1 /tmp/case_n
```
