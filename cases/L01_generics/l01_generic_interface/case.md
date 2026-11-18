---
id: l01_generic_interface
dim: L01_generics
priority: P0
sources:
  - sqlc@v1.28.0: internal/endtoend/testdata/alias/postgresql/pgx/v5/go/db.go:14 生成 Querier 接口与方法集断言
  - ent@v0.14.4: entc/integration/hooks/hooks_test.go:755 生成代码强类型遍历方法返回类型与方法集
  - pgx@v5.7.4: pgxpool/pool_test.go:111 Acquire/Release 生命周期与接口状态机
tags: [L01, generics, generic-interface, interface-embedding, delegation, method-value, pointer-identity, gc]
requires: [generics]
runtime: "<3s"
---

# 场景

定义 `ReadOnly[T]` 与嵌入它的 `Store[T]` 两个泛型接口：`memStore[T]` 是泛型实现，`audited[T]` 委托包装另一个 `Store[T]` 并计数，`payloadStore` 是非泛型类型直接实现 `Store[payload]` 这一具体实例。三种实现装进 `[]Store[payload]` 接口切片，GC 后逐个按 key 取回 `*payload`，校验指针身份、覆盖写返回旧指针、只读接口视图、接口方法值与删除后长度。最后以 `Store[string]` 二次实例化，确认不同 T 的接口形态与实现互不影响。

# 期望行为

1. 编译期三种实现均满足 `Store[payload]`（含嵌入的 `ReadOnly[payload]`）；
2. GC 后所有 key 经接口派发取回的指针与写入时相同，payload/blob 不变；
3. 覆盖写 Put 返回旧指针，新指针在 GC 后仍可读到；
4. 方法值 `fnGet/fnPut` 与 `ReadOnly` 视图在 GC 后调用结果正确；
5. 删除每 5 个 key 后 Len 精确下降、被删项不可见、存活项指针不变，委托包装计数（gets/puts/deletes）符合调用次数；
6. `Store[string]` 三个 key 的 payload 在 GC 后内容正确。

# 失败判读

- `store[i] pointer identity changed`：接口值持有的指针被破坏，检查 itab/方法派发路径；
- 编译错误 `missing method`：泛型接口方法签名与实现的 `*T` 形式不一致；
- `audited counters`：委托包装是否每调用都转发，注意不要在包装内复制值。

# 参数

- `UAF_SEED`：决定元素数量（160+seed%32），失败输出带种子；
- 无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_interface/*.go
UAF_SEED=7 GODEBUG=clobberfree=1 /tmp/case
```
