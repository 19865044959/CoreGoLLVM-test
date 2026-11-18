---
id: l06_iface_convert_matrix
dim: L06_reflect_iface
priority: P2
sources:
  - fiber@v2.52.6: router_test.go:306 断言 *App/*Group 均实现 Router
  - gin@v1.10.0: response_writer_test.go:107 Hijacker/CloseNotifier 类型断言
  - echo@v4.13.4: response_test.go:77 不支持 Flusher 时的断言失败路径
  - caddy@v2.10.0: modules/caddyhttp/responsewriter_test.go:47 接口包装与动态分派
tags: [L06, interface-conversion, type-assertion, Implements, ConvertibleTo, GC, torture]
requires: [reflect]
runtime: "<60s"
---

# 场景

定义 Reader/Writer/ReadWriter/Closer/ReadWriteCloser/Named/error 组合及值/指针接收者实现者，
对 23 组接口转换做 `Implements/AssignableTo/ConvertibleTo` 三重矩阵校验；运行时执行
interface→interface 断言、显式转换、失败断言 panic、typed nil 断言与反射方法调用。每轮 GC 后
重新检查动态类型、矩阵与调用结果。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_iface_convert_matrix`；
2. 所有矩阵项的三项判定与预期完全一致（包括接口→子接口为 true、any→具体接口为 false）；
3. 接口方法计数正确；comma-ok 断言矩阵（含 nil 接口、error、typed nil）全部符合预期；
4. 显式接口转换保留动态类型，`reflect.TypeOf(&iface).Elem()` 得到声明接口类型；
5. 非 comma-ok 失败断言 panic 被 recover；
6. 默认 100 轮在 60s 内完成，支持 `TORTURE_ROUNDS` 放大；五个 profile 结果一致。

## 失败判读

- `ConvertibleTo/AssignableTo` 与 `Implements` 不一致：转换/赋值规则实现错误；
- 断言 panic/结果错：接口动态类型或 itab 查询错误；
- 反射方法调用结果错：接口 Value 方法派发错误；
- GC 后动态类型丢失：接口数据区扫描问题。

## 参数

- `UAF_SEED`：随机种子，决定矩阵检查顺序与字段值；失败输出带出该值；
- `TORTURE_ROUNDS`：轮数，默认 100，上限 20000。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_iface_convert_matrix/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=2000 UAF_SEED=2 /tmp/case
```
