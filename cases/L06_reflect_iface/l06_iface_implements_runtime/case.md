---
id: l06_iface_implements_runtime
dim: L06_reflect_iface
priority: P1
sources:
  - caddy@v2.10.0: modules/caddyhttp/responsewriter_test.go:47 包装器按 io.ReaderFrom 断言分派
  - echo@v4.13.4: bind_test.go:336 reflect.Type.Implements(TextUnmarshaler) 探测
  - echo@v4.13.4: response_test.go:77 不支持 Flusher 时的断言失败路径
tags: [L06, reflect, Implements, AssignableTo, ConvertibleTo, io-interfaces, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

实现 io.Reader/io.Writer/io.Closer/io.ReadWriteCloser/error 的不同组合（指针接收者、值接收者、
单方法、多方法），构造 `Implements/AssignableTo/ConvertibleTo` 的 23 项矩阵并在运行时逐项校验。
再通过真实的接口值做直接调用、反射调用、接口到接口断言、typed nil 断言与失败断言 panic 路径，
全部跨显式 GC 重复。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_iface_implements_runtime`；
2. 对接口目标类型，`Implements/AssignableTo/ConvertibleTo` 三者与预期矩阵完全一致；
3. 接口方法数/方法名集合正确（Reader/Writer/Closer 各 1，ReadWriteCloser 3，any 0，error 1）；
4. 动态派发、`reflect.Value.MethodByName` 调用、接口↔接口断言结果正确；
5. typed nil 接口非 nil、动态类型满足接口、comma-ok 断言成功；
6. 失败断言 panic 被 recover；GC 后类型矩阵与数据不变，五个 profile 一致。

## 失败判读

- 三项矩阵判定不一致：接口方法集/itab 查询逻辑错误；
- 断言 panic 或结果反转：接口转换实现错误；
- 反射调用结果错：接口 Value 的方法派发或参数传递错误；
- GC 后 `*bufRW` 字段损坏：接口数据区扫描/写屏障问题。

## 参数

- `UAF_SEED`：随机种子，决定矩阵检查顺序；失败输出带出该值。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_iface_implements_runtime/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
