---
id: l11_devirtualize
dim: L11_compiler_opts
priority: P1
sources:
  - grpc-go@v1.71.1: benchmark/primitives/primitives_test.go:449 接口方法调用热循环
  - grpc-go@v1.71.1: benchmark/primitives/primitives_test.go:458 具体类型方法调用（去虚化/内联）
  - echo@v4.13.4: echo_test.go:1220 reflect.ValueOf(e).MethodByName("GET").Call(...) 与直接调用对照
tags: [devirtualize, interface-dispatch, itab, method-value, type-switch, typed-nil, gc]
requires: []
runtime: "<10s"
---

# 场景

模拟 HTTP 框架/RPC 中接口动态分派与编译器去虚化的等价性：`shape` 接口由 `*rect`（指针接收者）、`square`（值接收者）、`*circle`（指针接收者）实现。构造 30 个混合形状，在 GC 后分别通过接口切片求和、方法值切片调用、type switch、1000 轮热循环调用，并与直接具体类型调用的结果互相对照；另外验证 nil 接口调用与 typed nil 接收者调用必须 panic 且 panic 属于 `runtime.Error`。

# 期望行为

1. 直接调用 `rect.Area()` 与经接口调用的结果在 4×4 组宽高下完全一致；
2. 30 个混合形状经接口求和在 GC 后等于期望值；
3. 方法值 `sh.Area` 在 GC 后调用求和正确；
4. type switch 对 `*rect`/`square`/`*circle` 命中正确分支；
5. 1000 轮接口派发总和等于单轮期望值 ×1000；
6. `Name()` 按动态类型返回 rect/square/circle；
7. nil 接口与 typed nil 接口调用均 panic 且为 `runtime.Error`；typed nil 接口本身不等于 nil；
8. 最后一行输出 `ALL PASS l11_devirtualize`。

# 失败判读

- `direct vs interface rect mismatch`：去虚化后调用了错误实现或传参错误；
- `dispatch sum mismatch` / `hot interface dispatch` / `method value ...`：itab/方法值在 GC 后失效或派发到错误方法；
- `type switch dispatch mismatch`：接口类型元数据错误；
- `panic is not runtime.Error`：nil 派发路径行为与 Go 规范不符；
- `typed nil interface must not equal nil`：接口 nil 判定错误。

# 参数

- 无随机路径，不读取 `UAF_SEED`；
- 不要求去虚化一定发生；接口调用与直接调用结果必须一致。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_devirtualize/*.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case0 ./cases/L11_compiler_opts/l11_devirtualize/*.go && /tmp/case0
```
