---
id: l11_devirt_iface_array
dim: L11_compiler_opts
priority: P2
sources:
  - echo@v4.13.4: binder_test.go:2102 TestValueBinder_BindUnmarshaler 多种 Unmarshaler 实现的接口分派
  - gorm@v1.30.0: schema/callbacks_test.go:22 TestCallback 反射探测生命周期回调方法集
  - grpc-go@v1.71.1: benchmark/primitives/primitives_test.go:449 BenchmarkInterfaceNop 接口方法调用热路径
tags: [devirtualize, interface-field, interface-array, array-copy, value-copy, embedded-interface, typed-nil, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 4 轮；TORTURE_ROUNDS=50 时 200 轮）"
---

# 场景

模拟绑定器、ORM 回调与 RPC 原语中“接口字段位于数组/嵌套结构体”的去虚拟化边界：构建 12 个含接口字段的结构体数组与接口数组，经整体按值传递、数组拷贝、嵌套结构体拷贝后调用；拷贝中的 tag/int 字段互不影响，接口字段重绑定不回流，指针实现共享同一对象；数组末尾放置 typed nil 的 nil-safe 实现，最后显式 GC 后仅由数组保活的实现仍可正确分派。

# 期望行为

1. 结构体数组与接口数组的接口方法调用和、计数均等于按具体类型分支计算的期望值；
2. 数组拷贝后修改副本的 tag/int 不影响原数组；副本重绑定接口字段只在副本生效；
3. 指针实现（`*ifTimes`）在数组拷贝后保持指针身份相同，接口数组拷贝的分派结果不变；
4. 同一接口字段依次重绑定 plus/times/nil-safe/embedded 实现时，每次调用结果与 `Name()` 正确（含 nil-safe 的 `-55`）；
5. 嵌套结构体拷贝后原 inner 的接口分派不变，副本重绑定只影响副本，tag 相互隔离；
6. 零值接口字段为 nil，调用必须 panic；
7. 显式 GC 后结构体数组、接口数组与 typed nil 元素仍按期望分派；
8. 所有轮次在最终 GC 后逐项复算一致；
9. 最后一行输出 `ALL PASS l11_devirt_iface_array`。

# 失败判读

- `array interface field dispatch` / `interface array dispatch`：数组中的接口字段被错误去虚化或方法集计算错误；
- `array copy isolates value fields` / `nested copy isolates tag`：数组按值拷贝被错误改成共享存储；
- `array copy preserves original interface`：拷贝重绑定污染了原数组的接口字段；
- `nil interface field apply` 未 panic：nil 接口字段被错误内联为普通调用；
- `retained interface arrays after final GC`：数组未正确保活接口实现对象；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定每轮参数 x 与实现系数，失败输出带种子；
- `TORTURE_ROUNDS`：轮数倍数，默认 1（4 轮），上限 50。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_devirt_iface_array/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 UAF_SEED=3 GOGC=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_devirt_iface_array/*.go
UAF_SEED=1 /tmp/case_n
```
