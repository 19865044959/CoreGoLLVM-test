---
id: l06_type_identity_composite_deep
dim: L06_reflect_iface
priority: P2
sources:
  - gorm@v1.30.0: schema/field_test.go:305 type alias 与 defined type 的字段解析差异
  - protobuf-go@v1.36.6: internal/impl/enum_test.go:17 枚举接口 Type().New 的类型身份
  - pgx@v5.7.4: pgtype/pgtype_test.go:527 类型化 encode plan 缓存中的类型混淆回归
  - gorm@v1.30.0: schema/schema_test.go:135 高级数据类型（命名底层类型）的 schema 解析
tags: [L06, reflect, type-identity, composite-type, named-type, interface-identity, StructOf, FuncOf, type-map-key, GC, torture]
requires: [reflect]
runtime: "<60s"
---

# 场景

序列化/ORM/驱动缓存以 `reflect.Type` 为键，复合类型构造必须与字面类型严格同一，匿名接口按
方法集合（与声明顺序无关）判定身份。本用例覆盖命名指针与指针别名、数组长度与嵌套、map key
命名类型、通道方向嵌套、变参函数签名、`StructOf` 字段顺序/字段名/匿名标记、递归命名类型，
并把类型本身用作 map 键、在 GC 后复用。

# 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_type_identity_composite_deep`；
2. 匿名接口 `interface{A();B()}` 与 `interface{B();A()}` 相等，`interface{namedI1;namedI2}`
   与直接声明同方法集的匿名接口相等；具名接口仍与其匿名结构类型不相等；
3. 命名指针 `type nodePtr *node` 与 `*node` 不相等但可双向赋值，指针别名相等；
4. 数组长度/元素类型/嵌套、map key 命名类型、通道方向嵌套、变参函数（末位切片 + Variadic）
   的身份与 `ArrayOf/MapOf/ChanOf/FuncOf` 构造结果一致；
5. `StructOf` 的字段顺序、字段名与 Anonymous 标记参与身份，相同字段顺序命中同一缓存类型；
6. 递归命名类型 `Next` 的 Elem 指回自身，不同递归命名类型不相等；
7. `reflect.Type` 作 map 键按身份哈希，`StructOf/MapOf` 重新构造的等值类型命中同一键，跨 GC
   后类型身份与 map 查找不变；
8. 默认 8 轮，`TORTURE_ROUNDS` 可放大，所有 profile 输出一致。

# 失败判读

- `... identity` / `... differ`：类型身份判定错误（缓存未命中、tag/顺序/命名被忽略或误合并）；
- `unnamed interface identity ignores method order`：接口方法集被按声明顺序比较；
- `named pointer assignment rules`：命名指针赋值/转换规则错误；
- `type map lookup by equal type`：`reflect.Type` 的哈希/相等实现错误；
- `... after GC`：类型描述符或类型 map 未被保活；
- panic：`FuncOf/StructOf` 参数非法，通常是用例构造错误或类型缓存损坏。

# 参数

- `UAF_SEED`：由类型构造的值的标量来源；失败行带出种子；
- `TORTURE_ROUNDS`：轮数（默认 8，上限 2000）。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_type_identity_composite_deep/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=200 UAF_SEED=7 /tmp/case
```
