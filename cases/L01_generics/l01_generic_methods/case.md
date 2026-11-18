---
id: l01_generic_methods
dim: L01_generics
priority: P0
sources:
  - ent@v0.14.4: entc/integration/hooks/hooks_test.go:724 泛型 interceptor Limiter[Q interface{ Limit(int) }] 复用于不同类型并验证方法集派发
  - ent@v0.14.4: entc/integration/hooks/hooks_test.go:755 TestTypedTraverser 生成代码的强类型链式方法返回类型与方法集
  - sqlc@v1.28.0: internal/endtoend/testdata/alias/postgresql/pgx/v5/go/db.go:14 生成 Querier 接口 + var _ Querier 方法集断言
tags: [L01, generics, methods, generic-interface, embedding, dispatch, gc]
requires: [generics]
runtime: "<3s"
---

# 场景

定义泛型接口 `Collection[T]` 与两个实现 `Stack[T]`/`Queue[T]`，并用 `Deque[T]` 嵌入 `*Stack[T]` 验证方法提升后仍满足接口。分别经接口变量、接口方法值、泛型函数 `drain[T]` 三条路径在 GC 后派发，覆盖 LIFO/FIFO 顺序与指针身份。对应 ent 泛型 interceptor 复用与 sqlc 生成接口的方法集断言场景。

# 期望行为

1. 编译期 `var _ Collection[...]` 断言通过（Stack/Queue/嵌入 Deque）；
2. GC 后接口 `Len/Push/Pop` 与绑定的方法值都返回正确对象，payload 与 `next` 链完好；
3. Stack 弹出顺序为 LIFO、Queue 为 FIFO，指针身份与输入 slice 一致；
4. 嵌入提升的 `Deque[record]` 经接口派发顺序正确；
5. 空容器 `Pop()` 返回 nil；`Collection[int64]` 独立实例化派发正确；
6. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_generic_methods`。

# 失败判读

- `method value dispatch returned wrong pointer`：泛型方法值闭包捕获的 receiver/类型实参被破坏；
- `LIFO/FIFO ... corrupted`：接口 itab 指向错误实例化或方法体读错字段；
- `... payload corrupted after GC`：receiver 结构体里的 `[]*T` 未被扫描；
- 编译失败：泛型接口/嵌入泛型实例化的方法集判定缺陷。

# 参数

- `UAF_SEED`：决定元素数（64..72），失败输出带种子。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_generic_methods/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
