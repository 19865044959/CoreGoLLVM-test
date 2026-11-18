---
id: l01_constraint_methods
dim: L01_generics
priority: P0
sources:
  - ent@v0.14.4: entc/integration/hooks/hooks_test.go:724 泛型 interceptor `Limiter[Q interface{ Limit(int) }]` 以方法作为约束
  - ent@v0.14.4: dialect/gremlin/encoding/graphson/marshaler_test.go:62 Unmarshaler 接口方法派发与错误传播
  - sqlc@v1.28.0: internal/endtoend/endtoend_test.go:107 生成模型的 Scan/Value 等方法集
tags: [L01, generics, type-constraint, methods, comparable, union, pointer-receiver, gc, UAF_SEED]
requires: [generics]
runtime: "<3s"
---

# 场景

验证三类带方法的约束：(a) 纯方法接口约束 `keyed{ Key() int64; Label() string }`，`maxKey/sumKeys/findByLabel` 以 `*entity` 实例化，实体带 `next` 指针链；(b) `comparable + keyed` 组合约束，把实体值作为 map key 建立索引；(c) `weighted{ ~int64 | ~int32; Weight() int64 }` 方法 + 底层类型 union，分别实例化自定义类型 `w64/w32`。UAF_SEED 打乱呈现顺序，GC 后校验最大 key 指针、标签查找、索引命中与两种数值实例的加权和。

# 期望行为

1. `sumKeys` 等于按独立循环复算的 id 总和；GC 后 `maxKey` 返回 id 最大的同一 `*entity`，字段正确；
2. `findByLabel` 命中时返回原始指针（GC 后身份不变），不存在的标签返回 ok=false 且不 panic；
3. `indexByKey` 的值 key 映射在 GC 后仍能按实体值查找，位置与打乱顺序一致；
4. `totalWeight[w64]`、`totalWeight[w32]` 的加权和精确，方法在 union 各成员上派发正确；
5. 空切片调用 `maxKey` 返回零值且 ok=false。

# 失败判读

- 编译失败提示 method set：检查约束接口与接收者（值/指针）是否匹配；
- `pointer identity changed`：约束方法的调用路径未保活对象，重点看 next 链与全局 sink；
- `totalWeight mismatch`：方法+union 约束的实例化分派错误，核对 `~` 底层类型。

# 参数

- `UAF_SEED`：决定实体数量（180+seed%20）与呈现顺序；期望值独立推导，失败输出带种子；
- 无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_constraint_methods/*.go
UAF_SEED=7 GODEBUG=clobberfree=1 /tmp/case
```
