---
id: l01_nested_generics
dim: L01_generics
priority: P0
sources:
  - pgx@v5.7.4: pgtype/multirange_test.go:20 Multirange[Range[Int4]] 嵌套泛型实例化与 nil/空/两段区间
  - ent@v0.14.4: entc/integration/hooks/hooks_test.go:755 生成代码的强类型链式查询方法（嵌套类型返回）
  - grpc-go@v1.71.1: resolver/map_test.go:74 AddressMap 以带指针字段的 Address 为键，属性用接口比较（嵌套引用语义）
tags: [L01, generics, nested-instantiation, pointer-chain, multi-type-params, gc]
requires: [generics]
runtime: "<3s"
---

# 场景

构造 `Box[Box[*payload]]` 三层嵌套、`Box[Box[Box[*payload]]]` 四层链，以及双类型参数 `Pair[Box[*payload], int64]`，模拟 pgx `Multirange[Range[T]]` 的嵌套实例化与 ent 强类型链式返回。GC 后逐层回读指针地址、tag 与叶子 payload，并经由最内层写穿；另批量存活 32–40 条嵌套链、检查 nil 嵌套字段。

# 期望行为

1. 每层 `Get()` 返回的指针与创建时同一地址，tag 正确；
2. 叶子 payload 的 `v/name` 在 GC 后不变，三层遍历的 `**payload` 能解引用并比较；
3. `Pair[Box[*payload], int64]` 的两个字段在 GC 后分别保持嵌套指针与标量写穿；
4. 经嵌套链写穿后，所有层视图读到更新值；
5. nil 嵌套字段 GC 后仍为 nil；
6. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_nested_generics`。

# 失败判读

- `outer lost inner box` / `third-level traversal mismatch`：嵌套实例化的字段偏移或指针层数计算错误；
- `nested payload corrupted after GC`：多层间接指针未被栈/堆扫描；
- `Pair.a ...`：多类型参数实例化的字段布局混用；
- `nil nested box became non-nil`：空指针层被写屏障/零化逻辑改写。

# 参数

- `UAF_SEED`：决定批量嵌套链数量（32..40），失败输出带种子。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_nested_generics/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
