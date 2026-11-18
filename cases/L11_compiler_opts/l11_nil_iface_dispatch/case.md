---
id: l11_nil_iface_dispatch
dim: L11_compiler_opts
priority: P0
sources:
  - grpc-go@v1.71.1: attributes/attributes_test.go:71 typed nil 实现 Stringer 与未实现 Stringer 的接口装箱
  - grpc-go@v1.71.1: codes/codes_test.go:59 TestUnmarshalJSON_NilReceiver nil 接收者方法调用
  - goccy-json@v0.10.5: encode_test.go:1055 TestNilMarshalerTextMapKey nil Marshaler 作为 map 键
tags: [nil-interface, typed-nil, nil-receiver, devirtualize, interface-dispatch, embed-interface, nil-func, error-interface, errors-as, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 gRPC attributes、codes 与 JSON Marshaler 中的 nil 接口/typed nil 分派边界：nil 接口调用必须 panic；装有不安全 nil 指针的接口本身非 nil，nil-safe 方法可正常返回，不安全方法 panic；接口相等、type switch、嵌入接口、struct 接口字段重置以及 typed nil func/error 的装箱、断言、`errors.As/Is` 都被逐项验证。

# 期望行为

1. 零值接口 `== nil`，调用其方法 panic；typed nil 指针装入接口后 `!= nil`；
2. nil-safe 接收者经接口返回固定 `-7` / `"nil-impl"`；不安全接收者的取值方法 panic，但不解引用的方法正常返回；
3. 同动态类型的 typed nil 接口相等，不同动态类型不等；
4. type switch 对 nil 接口进入 `nil` 分支，对 typed nil 进入对应具体类型且值为 nil；
5. 零接口字段与零嵌入接口调用 panic；赋入 typed nil 后按 nil-safe 方法工作，重新绑定与重置语义正确；
6. typed nil func 装箱后接口非 nil，断言得到 nil func，调用 panic；
7. typed nil `*ndErr` 作为 error 非 nil，`Error()` 返回 `"nil-error"`，`errors.As` 成功且目标为 nil，包装后 `errors.Is/As` 仍成立；
8. typed nil slice 装箱后非 nil，断言得 nil slice 且可 append；
9. 显式 GC 后 typed nil 与非 nil 实现接口均保持语义；
10. 最后一行输出 `ALL PASS l11_nil_iface_dispatch`。

# 失败判读

- `nil interface method call must panic`：nil 接口被错误地当作可调用；
- `typed nil safe receiver` / `typed nil strict receiver`：接口动态类型丢失或 nil 接收者语义被改写；
- `typed nil func assertion` / `typed nil error interface`：typed nil 装箱被折叠成 nil 接口；
- `errors.As typed nil error`：错误链遍历对 typed nil 处理错误；
- `typed nil interfaces after GC`：接口值被错误清零；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定值实现参数 5..21，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P0，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_nil_iface_dispatch/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_nil_iface_dispatch/*.go
UAF_SEED=1 /tmp/case_n
```
