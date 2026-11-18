---
id: l11_nil_func_iface
dim: L11_compiler_opts
priority: P0
sources:
  - goccy-json@v0.10.5: decode_test.go:254 null 解码到 func 值必须得到 nil func
  - gorm@v1.30.0: schema/field_test.go:92 指针字段 Setter 的 nil 清零语义
  - kitex@v0.13.0: pkg/remote/codec/thrift/codec_apache_test.go:56 全局 read/write/check 函数指针注册与覆盖
tags: [nil-func, nil-check, typed-nil, interface, defer, panic-recover, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 JSON null 解码、ORM 指针字段与编解码全局函数指针中的 nil 函数检查：nil func 比较、nil 函数字段、map 缺失键返回 nil func、函数切片中的 nil 项、typed nil func 装入接口、函数返回 nil func、`defer` nil func 的 panic/recover，以及显式 GC 后非 nil 函数值仍可调用。调用 nil func 必须 panic，经 recover 捕获的文本必须含 `nil pointer dereference`。

# 期望行为

1. 零值 func 等于 nil，helper 的 nil 检查路径返回 -1；
2. 赋值后函数值在 GC 后仍可调用；
3. 结构体 nil 函数字段为 nil，赋值与 GC 后调用正确，重置回 nil 后仍为 nil；
4. map 缺失键返回 nil func 且 comma-ok 为 false；
5. 三元素函数切片中 nil 计数为 1，非 nil 调用求和正确且 GC 后不变；
6. 调用切片中的 nil func panic 并被 recover；
7. typed nil func 装入接口后接口 `!= nil`，断言成功且函数值为 nil，经接口调用 panic；
8. 空接口等于 nil；函数返回 nil func 时直接比较为 nil、装箱后接口非 nil；
9. 普通 panic 与正常路径的 recover 结果正确；
10. `defer f()` 中 f 为 nil 时在返回阶段 panic 并被更早注册的 defer recover；
11. 最后一行输出 `ALL PASS l11_nil_func_iface`。

# 失败判读

- `zero func value is nil` / `nil func checked before call`：nil 函数检查被错误消除；
- `func value after GC` / `func struct field after GC`：函数值或闭包环境未被保活；
- `typed nil ...`：接口 nil 判定与类型断言语义错误；
- `calling nil func` / `deferred-nil`：nil 调用未 panic 或 panic 控制流被破坏；
- `missing map func` / `slice func nil count`：nil 容器元素语义错误；
- 失败输出带 `UAF_SEED`。

# 参数

- 无随机路径，读取 `UAF_SEED` 仅用于失败输出；固定规模。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_nil_func_iface/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_nil_func_iface/*.go
UAF_SEED=1 /tmp/case_n
```
