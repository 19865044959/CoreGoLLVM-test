---
id: l06_type_assert_matrix
dim: L06_reflect_iface
priority: P0
sources:
  - fiber@v2.52.6: router_test.go:306 *App/*Group 断言为 Router 接口
  - gin@v1.10.0: response_writer_test.go:107 Hijacker/CloseNotifier 类型断言与 panic 路径
  - echo@v4.13.4: response_test.go:77 不支持 Flusher 时的断言失败 panic
  - caddy@v2.10.0: modules/caddyhttp/responsewriter_test.go:47 按 io.ReaderFrom 断言分派
tags: [L06, type-assertion, interface, method-set, unexported-method, typed-nil, panic, GC]
requires: [reflect]
runtime: "<3s"
---

# 场景

构造 20 个动态类型（值/指针接收者、嵌入提升、未导出方法、typed nil、error/Stringer、标量、func、
chan、nil 接口）× 8 个接口目标（any、reader、writer、readWriter、secretReader、error、
fmt.Stringer、仅未导出方法接口）的 160 项断言矩阵，逐项与预期和 `reflect.Type.Implements`
对照；再覆盖接口到接口断言、方法数、typed nil、nil 接口与非 comma-ok 断言的 panic 语义。

## 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_type_assert_matrix`；
2. 160 项 comma-ok 断言与预期完全一致，其中 48 项为 true；
3. `reflect.Type.Implements` 与运行时断言逐项一致；
4. 值/指针接收者方法集、嵌入提升与未导出方法接口判定符合 Go 规则；
5. 结构相同但不同名的接口类型可互相赋值，动态类型满足两个接口；
6. typed nil 指针断言成功且结果为 nil 指针；nil 接口断言失败；
7. 非 comma-ok 失败断言必须 panic 并被 recover；GC 后矩阵与指针负载不变。

## 失败判读

- `assert <类型> -> <目标>`：itab/方法集判定错误，重点检查未导出方法与提升方法；
- `Implements ...`：编译期接口实现关系与运行时断言不一致；
- `typed nil ...`：typed nil 的 itab 缓存或接口数据区错误；
- `... did not panic`：失败断言未触发 runtime panic；
- `pointer payload after GC`：断言得到的接口数据指针在 GC 后失效。

## 参数

- `UAF_SEED`：矩阵中基础整数值来源；失败行带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_type_assert_matrix/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
