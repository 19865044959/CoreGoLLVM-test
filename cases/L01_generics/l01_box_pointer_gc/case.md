---
id: l01_box_pointer_gc
dim: L01_generics
priority: P0
sources:
  - pgx@v5.7.4: rows_test.go:371 RowToAddrOf[int32] 返回 *int32，多个结果地址互异且跨 GC 有效
  - sqlc@v1.28.0: internal/endtoend/testdata/emit_enum_valid_and_values/go/models.go:32 生成物 Null[T] 包装值指针
  - ent@v0.14.4: entc/gen/globalid_test.go:116 must[T any] 多类型实例化辅助
tags: [L01, generics, pointer-field, instantiation, gc, write-barrier]
requires: [generics]
runtime: "<3s"
---

# 场景

`Box[T]` 持有一个 `*T` 字段，模拟 pgx `RowToAddrOf[T]` / sqlc `Null[T]` 这类"泛型包装值指针"的生成代码。用 `Box[payload]`（payload 内部还有 `child *payload` 链）和 `Box[int64]` 两种实例化建约 100 个对象，全局 sink 保活，跨两轮显式 GC 校验指针身份、写穿值与 nil 字段；随后 `Replace` 换指针再 GC。

# 期望行为

1. GC 后每个 `Box[payload]` 的指针与创建时指针相同（身份不变），payload 的 `v`/`name`/`child` 全部正确；
2. nil 指针字段在 GC 后仍为 nil，`Valid()` 为 false；
3. `Replace` 返回旧指针，新指针在 GC 后仍可读到 777；
4. `Box[int64]` 的写穿（`*Get() = 43`）跨 GC 生效；
5. 尾节点 child 链深度等于 n；
6. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_box_pointer_gc`。

# 失败判读

- `box pointer identity changed across GC`：`Box` 的 `*T` 字段未被栈/堆扫描，指针被回收或改写；
- `payload child link broken`：间接指针链上的对象被提前回收（写屏障/存活判定问题）；
- `nil box became valid`：零值/空指针状态被 GC 破坏；
- clobberfree 下 payload 字段出现固定毒值：典型 UAF。

# 参数

- `UAF_SEED`：决定 payload 数量（96..112），失败输出带种子；无随机数据内容。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_box_pointer_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
