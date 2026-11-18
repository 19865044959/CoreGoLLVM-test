---
id: l16_reflect_struct_tag
dim: L16_template_interp
priority: P0
sources:
  - gorm@v1.30.0: schema/schema_helper_test.go:47 从 reflect 字段读取 gorm tag 并解析设置
  - ent@v0.14.4: entc/integration/integration_test.go:288 用 reflect.Type.FieldByName 读取自定义 tag
  - ent@v0.14.4: entc/integration/integration_test.go:291 读取 json:"-" tag
  - sonic@v1.13.2: fuzz/struct_fuzz_test.go:73 generateJSONTag 构造 reflect.StructTag 变体
tags: [L16, reflect, struct-tag, json, embedded-field, unexported-field, GC]
requires: [reflect]
runtime: "<2s"
---

# 场景

一个含 6 个字段的 struct 同时带 `json`/`db`/`validate`/`xml` tag、`omitempty`、`json:"-"`、匿名嵌入与未导出字段。用 reflect 检查字段顺序/偏移/索引、tag Get/Lookup、逗号选项、转义引号与非法 tag；用 `reflect.New` 反射构造对象（未导出字段不可写），再用 encoding/json 验证 tag 行为。每轮 GC 后重读同一 `reflect.Type` 的 tag 并再次 marshal，校验类型元数据与对象内容稳定。

# 期望行为

1. NumField=6，字段顺序为 ID,Name,Hidden,Meta,inner,Embedded，偏移严格递增，Index 末位与字段序号一致；
2. ID 的 json=`id`、db=`id,primary`、validate=`required,min=1`；Name 的 json 选项为 `name,omitempty`；Hidden 为 `-`；
3. 未导出 inner 有 PkgPath 且 `IsExported()==false`，其 tag 仍可读取；匿名 Embedded 的 `Anonymous=true`；
4. 提升字段 Extra 的 Index 为 `[5 0]`，json/xml tag 均为 `extra`；`FieldByIndex([3 0])` 得到 Meta.Ver；
5. `StructTag(`json:"a\"b"`)` 的 Lookup 返回 `a"b`；非法 tag Lookup 返回 false；多键 tag 可分别读取；
6. 反射构造对象后 marshal 为 `{"id":1,"name":"n","meta":{"ver":7},"extra":"e"}`，Name 清空后 omitempty 生效且 `json:"-"` 字段不出现；
7. GC 后 `reflect.TypeOf(*rec)` 仍等于缓存 Type，tag/JSON 输出不变；
8. 最后一行 `ALL PASS l16_reflect_struct_tag`。

# 失败判读

- `field names/offset/index`：字段布局或匿名嵌入元数据错误；
- `tag ... want`：StructTag 解析（逗号、转义、非法键）与标准库不一致；
- `not settable` / `settable`：导出性/可设置性判定错误；
- `json ... want`：json tag/omitempty/"-" 行为被破坏；
- `type identity changed after GC`：类型描述符生命期异常（严重运行时问题）；
- 超时：不应发生。

# 参数

- `UAF_SEED`：决定轮数（3–5）与字段随机访问顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_reflect_struct_tag/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GODEBUG=efence=1 /tmp/case
```
