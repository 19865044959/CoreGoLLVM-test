---
id: l10_large_const_table
dim: L10_codegen
priority: P2
sources:
  - ent@v0.14.4: entc/gen/type_test.go:252 TestField_Constant 字段常量名表
  - ent@v0.14.4: entc/gen/type_test.go:299 TestBuilderField 生成标识符合法化
  - sqlc@v1.28.0: internal/endtoend/endtoend_test.go:107 + internal/endtoend/testdata/emit_enum_valid_and_values/go/models.go:12-65 枚举常量/名称表
  - protobuf-go@v1.36.6: internal/encoding/defval/default_test.go:17 常量默认值格式化
tags: [L10, L11, const-table, compile-time-folding, binary-search, GC, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大探测次数"
---

# 场景

模拟生成器输出的 1024 项大 const 表：`Code0000..Code1023` 与 `StrCode0000..StrCode1023` 两组常量、常量索引数组与名称数组；数组长度由常量表达式 `Code1023-Code0000+1` 给出，表总和由编译期常量公式核对。运行期用算术、`sort.Search` 二分、名称解析与 map 四条路径做反查，GC 后整表哈希/总和/映射不变；`UAF_SEED` 有效/无效混合探测。

## 期望行为

1. 编译期折叠：数组长度、表跨度、名称长度、表总和常量均正确；int32/int64/uint64 边界常量正确；
2. 1024 个整型常量值 = `1000000+i`，名称 = `CODE_%04d`，无重复；
3. 算术、二分、名称反查、map 四条路径对全部索引一致；
4. GC 后整表哈希、总和、映射与保留名称全部不变；
5. MinI32/MaxI32、0、负数、base-1、base+1024 等越界值被四条路径拒绝；畸形名称（空、大小写、越界编号、尾随空格）被拒绝；
6. `UAF_SEED` 随机探测 150000×`TORTURE_ROUNDS` 次（有效/无效混合），周期性 GC 且哈希不变；
7. 最后一行 `ALL PASS l10_large_const_table`。

## 失败判读

- 编译失败在常量表达式处：常量折叠/类型推导问题；
- 表总和或哈希错误：常量值/名称表生成错位；
- 某条查找路径不一致：索引/二分边界或名称解析实现错误；
- GC 后变化：保留的切片/字符串未正确保活（编译器问题）或用例别名 bug；
- 随机失败可凭 `seed=` 复现。

## 参数

- `UAF_SEED`：随机探测序列种子；
- `TORTURE_ROUNDS`：探测轮次倍数（默认 1，上限 50）。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_large_const_table/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 GODEBUG=clobberfree=1 /tmp/case
```
