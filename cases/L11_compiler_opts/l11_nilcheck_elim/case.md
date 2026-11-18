---
id: l11_nilcheck_elim
dim: L11_compiler_opts
priority: P0
sources:
  - goccy-json@v0.10.5: decode_test.go:2158 TestUnmarshalNulls JSON null 解到指针/slice/map 时清 nil
  - gorm@v1.30.0: schema/schema_test.go:38 TestParseSchemaWithPointerFields 全指针字段模型的反射递归解引用
  - gorm@v1.30.0: schema/field_test.go:162 TestAdvancedDataTypeValuerAndSetter 指针/接口值的 Setter/Getter 路径
tags: [nil-check-elimination, repeated-deref, pointer-chain, panic-recover, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 ORM 字段读取与 JSON 解码中的重复解引用：显式 `if n == nil` 之后的连续 `n.v`、跨 `runtime.GC()` 调用后的再次解引用、循环内逐元素 nil 检查、`**T` 双层解引用、指针链 `n.next.next`、切片下标与 nil 双重检查。混合切片按 `UAF_SEED` 决定 nil 模式，GC 后重新统计存活与 nil 数量。直接解引用 nil 指针必须 panic 且属于 `runtime.Error`，panic 文本含 `nil pointer dereference`。

# 期望行为

1. nil 守卫的重复解引用返回 -1/正确三倍值；
2. `doubleDeref` 对 nil 外层返回 -1、内层 nil 返回 -2、非 nil 返回值；
3. 混合切片的 nil 计数与求和正确，GC 后不变；
4. 循环 nil 检查对 nil 元素累加 100，非 nil 元素累加其值；
5. `safeIndex` 对负下标/上界返回 -1，nil 元素返回 -2；
6. 指针链在显式 nil 检查后取到第三节点值，短链/nil 链返回 -1；
7. 跨调用与 GC 后的非 nil 解引用结果正确，指针字段在 GC 后完整；
8. nil 解引用 panic 为 `runtime.Error` 且文本含 `nil pointer dereference`；
9. 最后一行输出 `ALL PASS l11_nilcheck_elim`。

# 失败判读

- `nil receiver guarded sum` / `non-nil repeated dereference`：nil 检查被错误消除导致返回值错误；
- `mixed slice nil checks`：循环内逐元素 nil 检查被错误提升/删除；
- `expected panic` / `panic is not runtime.Error` / `unexpected panic text`：nil 解引用未 panic 或 panic 来源错误；
- `pointer fields after GC`：GC 后对象被提前回收或字段被零化；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定混合切片长度 48..64 与 nil 模式，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P0，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_nilcheck_elim/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_nilcheck_elim/*.go
UAF_SEED=1 /tmp/case_n
```
