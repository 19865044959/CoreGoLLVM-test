---
id: l01_instantiation_reuse
dim: L01_generics
priority: P1
sources:
  - gorm@v1.30.0: tests/generics_test.go:21 gorm.G[User](DB) 泛型查询链在同一类型上多次构造并复用
  - gorm@v1.30.0: tests/generics_test.go:772 5 goroutine 复用同一条泛型查询链，各自条件不互相污染
  - pgx@v5.7.4: rows_test.go:335,371 同一 RowTo[int32]/RowToAddrOf[int32] 在多个调用点重复实例化
tags: [L01, generics, instantiation-identity, type-identity, reflect, gc]
requires: [generics]
runtime: "<10s"
---

# 场景

同一泛型类型 `Holder[T]` 在代码内两个独立构造点（模拟不同包/编译单元）实例化为 `Holder[payload]`，验证运行时类型身份共享、可互换赋值与 through-interface 断言；同时并行存在 `Holder[int64]`、`Holder[int32]`、`Holder[string]` 三种实例，各自持有 `*T`，跨多轮 GC 检查值与序号。对应 gorm 泛型查询链复用与 pgx `RowTo[T]` 多调用点实例化场景。

# 期望行为

1. 两个构造点的 `Holder[payload]` 满足 `reflect.TypeOf` 相等、`%T` 一致，可赋给同一变量且运行时断言成功；
2. 共享 payload 指针在 GC 后身份/内容不变；
3. 三种不同实例化的 `Holder` 类型互不相等，值和 `seq` 在 GC 后正确；
4. 放入 `[]any` 后再断言回各自实例，payload 未损坏；
5. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_instantiation_reuse`。

# 失败判读

- `same instantiation produced distinct types`：前端为同一实参生成了两份类型/字典，类型身份不一致；
- `distinct instantiations collapsed`：不同实参的实例被错误合并（类型形状混用）；
- `value corrupted`：泛型结构体字段偏移/指针位图按错误的实例计算；
- `lost identity through interface`：itab 或运行时类型元数据在 GC 后被破坏。

# 参数

- `UAF_SEED`：决定每种实例的轮数（60..80），失败输出带种子。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_instantiation_reuse/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
