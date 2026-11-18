---
id: l01_generic_inst_dedup
dim: L01_generics
priority: P1
sources:
  - pgx@v5.7.4: copy_from_test.go:833 RowTo[[]int64] 以切片本身作为类型参数，实例化元素类型为复合类型
  - gorm@v1.30.0: tests/generics_test.go:67 gorm.G[map[string]interface{}] 以 map 类型作为类型参数
  - ent@v0.14.4: entc/gen/globalid_test.go:116 must[T] 分别实例化为 int/string/struct/map 的泛型辅助函数
tags: [L01, generics, instantiation-dedup, type-identity, composite-type, function-value, concurrent-first-use, gc]
requires: [generics]
runtime: "<10s"
---

# 场景

同一实参 `Holder[item]` 在两个独立构造点、map/slice/chan/array/func/struct 复合类型、
泛型函数值与 4–8 个 goroutine 首次使用中反复实例化；另并存 `Holder[int8]`、
`Holder[uint8]`、`Holder[string]`、`Holder[[]byte]` 四种实参。验证运行时类型身份
去重、函数实例去重、方法集/接口断言与不同实例的独立布局。对应 pgx `RowTo[[]int64]`、
gorm `G[map[string]any]`、ent `must[T]` 的多调用点实例化。

# 期望行为

1. 两个构造点的 `Holder[item]` 与 `reflect.TypeFor[Holder[item]]()` 类型相等，可互相赋值；
2. 同一泛型函数 `ident[Holder[item]]` 在多处取值得到相同代码指针（实例化去重）；
3. 复合类型（map/slice/chan/[2]/func/struct）中的 `Holder[item]` 类型身份与 `TypeFor` 一致；
4. `Holder[int8]`/`Holder[uint8]`/`Holder[string]`/`Holder[[]byte]` 类型互不相等，
   经 `[]any` + 类型断言回读后字段偏移与指针载荷正确；
5. 并发 goroutine 观察到的类型、函数实例、payload 指针与 seq 在 GC 后一致；
6. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_generic_inst_dedup`。

# 失败判读

- `same instantiation produced distinct types`：同一实参生成了两份运行时类型/字典；
- `generic function instantiation was not deduplicated`：泛型自由函数的实例化未复用；
- `int8/uint8 instantiations collapsed`：不同实参被错误合并，字段布局可能混用；
- `... corrupted through interface`：实例化结构体字段偏移或指针位图错误；
- `concurrent ... mismatch`：并发首次实例化产生不一致的类型/代码指针；
- `FAILED ... seed=`：失败输出带 UAF_SEED。

# 参数

- `UAF_SEED`：决定并发 worker 数（4..8）与每 worker 循环次数（80..119），失败输出带种子。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_generic_inst_dedup/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
