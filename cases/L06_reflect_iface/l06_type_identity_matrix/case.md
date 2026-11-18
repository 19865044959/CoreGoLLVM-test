---
id: l06_type_identity_matrix
dim: L06_reflect_iface
priority: P0
sources:
  - gorm@v1.30.0: schema/schema_test.go:135 命名类型/别名类型与 sql.Scanner 接口探测
  - gorm@v1.30.0: schema/field_test.go:305 type alias 与 defined type 的字段解析差异
  - pgx@v5.7.4: pgtype/pgtype_test.go:527 类型化 encode plan 缓存中的类型身份
  - protobuf-go@v1.36.6: internal/impl/enum_test.go:15 枚举接口 Type().New 的类型身份
tags: [L06, reflect, type-identity, TypeFor, named-type, alias, StructOf, interface-identity, Comparable, GC]
requires: [reflect]
runtime: "<3s"
---

# 场景

ORM 与序列化框架会以 `reflect.Type` 作为 schema/编码计划的缓存键，因此命名类型、别名、
复合类型构造、结构体 tag、接口结构同一性必须稳定且符合 Go 类型身份规则。本用例覆盖
`TypeFor`/`TypeOf`、defined type 与 alias、`SliceOf/MapOf/ChanOf/ArrayOf/FuncOf/PointerTo/
StructOf` 的同一性、tag 参与结构体身份、命名/匿名接口的相等与可赋值、`Comparable` 与
Name/PkgPath/String 元数据，并在 GC 后复用保存的类型构造值。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_type_identity_matrix`；
2. `TypeFor[T]` 与 `TypeOf(T{})`/指针 Elem 相等；alias 与底层类型是同一类型，defined type
   是不同的命名类型但可 Convert；
3. `SliceOf/ArrayOf/MapOf/ChanOf/FuncOf/PointerTo/StructOf` 生成的类型与字面类型完全相等，
   相同字段不同 tag 的 StructOf 是不同的结构体类型；
4. 命名接口与结构相同的另一命名接口不相等但可双向赋值；匿名结构相同接口类型相等；
   宽接口 Implements/AssignableTo 窄接口，反向不成立；
5. `Comparable` 矩阵正确（slice/map/func 不可比较，数组/可比较字段结构体/接口/chan/指针可比较）；
6. Name/PkgPath/Kind/String/Elem/Key/In/Out/Len/NumField 元数据符合预期；
7. 类型描述符及由保存的 Type 构造的 struct/slice/map 在显式 GC 后身份与内容不变。

## 失败判读

- `defined type differs from underlying` / `alias identity`：命名/别名判定错误；
- `SliceOf identity` / `StructOf ... identity`：复合类型构造未命中类型缓存或 tag 未参与身份；
- `named interfaces ... assignable` / `Implements`：接口方法集或赋值规则错误；
- `Comparable ...`：可比较性判定错误；
- `... after GC`：类型描述符或由 Type 构造的值未保活。

## 参数

- `UAF_SEED`：写入由保存 Type 构造的值的标量来源；失败输出带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_type_identity_matrix/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
