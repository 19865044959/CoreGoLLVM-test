---
id: l01_constraint_union
dim: L01_generics
priority: P0
sources:
  - protobuf-go@v1.36.6: internal/set/ints_test.go:24 泛型 Set[T ~uint32|~uint64] 的位图边界与 Set/Has/Clear
  - pgx@v5.7.4: pgtype/range_codec_test.go:19 Range[pgtype.Int4] 等以数值类型参数承载边界的泛型实例
  - kitex@v0.13.0: pkg/remote/trans/ttstream/container/queue_test.go:26 NewQueue[int] 泛型数值容器
tags: [L01, generics, type-constraint, union, tilde, pointer-payload, gc]
requires: [generics]
runtime: "<3s"
---

# 场景

用 `interface{ ~int64 | ~int32 | ~uint32 | ~uint16 }`（含自定义底层类型 `myInt`）和 `interface{ ~int64 | ~string }` 两类约束，构建泛型 `NumBox[T]`（持 `*T`）、泛型函数 `Sum[T]` 与 `CountKeys[K,V]`。对每种数值/键实例化写入指针、显式 GC，再验证写穿、求和与 map 指针身份。来源对应 protobuf-go 泛型位图集合与 pgx 数值区间类型的实例化语义。

# 期望行为

1. `Sum[int64/int32/uint32/uint16]` 在 GC 后结果与逐项预期一致；
2. `~int64` 约束接受自定义类型 `myInt`，`Sum[myInt]` 正确；
3. `NumBox[T].Add` 通过 `*T` 写穿，GC 后值更新可见且指针身份不变；
4. `CountKeys[int64/string/myKey,*entry]` 三种键实例化均返回 n，且取出的 `*entry` 是同一地址；
5. `Sum` 跳过 nil 指针元素；
6. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_constraint_union`。

# 失败判读

- `Sum[...] mismatch`：类型参数实例化后运算/解引用错误，或 `~` 底层类型被错误区分；
- `pointer identity changed`：约束实例化的指针字段未被 GC 扫描；
- `string-key map lost pointer identity`：不同键实例化共享了错误的类型形状；
- 编译期错误 `cannot use ... as T`：约束并集/`~` 支持缺陷（本用例用于暴露该类前端问题）。

# 参数

- `UAF_SEED`：决定每类实例的元素数（40..48），失败输出带种子。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_constraint_union/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
