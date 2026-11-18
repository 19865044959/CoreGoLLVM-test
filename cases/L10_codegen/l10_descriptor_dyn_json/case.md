---
id: l10_descriptor_dyn_json
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1493 反射视图代理具体消息，多轮 GC 后字段仍可读
  - protobuf-go@v1.36.6: internal/filedesc/desc_test.go:32 由描述信息构造字段/嵌套消息表
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:966/991 map 字段键值解码
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:184 JSON null 保持字段未设置
  - ent@v0.14.4: entc/gen/graph_test.go:565 生成前用 reflect.StructTag 校验字段 tag
tags: [L10, L06, descriptor, reflect, dynamic-json, presence, map-slice, enum, nested, unknown-field, gc, UAF_SEED]
requires: []
runtime: "<10s（UAF_SEED 驱动随机消息）"
---

# 场景

由反射解析 `pb:"<num>,<json>,<card>[,enum=name]"` 生成消息 descriptor：支持 optional 指针标量、`[]string`、`map[string]string`、int32 枚举与嵌套消息。descriptor 驱动一个动态 JSON codec：字段按编号输出、map 键排序、枚举名/数字互转、optional presence 与 null/缺失/空容器区分、未知字段 strict/lenient 策略、嵌套递归错误定位。构建期拒绝重复编号/JSON 名、非法 tag、未知枚举、不支持的类型、未导出字段、递归类型与错误基数；运行期拒绝类型错误、枚举越界、非有限浮点编码。

## 期望行为

1. 构建 8 字段主 descriptor 与 2 字段嵌套 descriptor，字段按编号排序，类型/嵌套关系正确；
2. 金标编码输出按字段号顺序，`attrs` 键排序，显式零指针（如 `count:0`）被输出，空 `[]`/`{}` 保留；
3. 编码 → GC → 解码 → 再编码逐字节稳定；输入 buffer 毒化后结果不变；
4. 缺失字段保持 nil 并省略；显式零/空/nil 与缺失可区分；JSON null 等价于未设置；
5. strict 拒绝顶层与嵌套未知字段，lenient 丢弃；枚举别名解码为 canonical 数值，未知数字保留，未知名字报错；
6. 16 类错误输入（类型、溢出、null 元素、尾随数据、非对象等）全部拒绝；
7. 13 类 descriptor 构建错误全部拒绝；
8. 反射类型与 descriptor 在 GC 后仍可用于编解码；
9. `UAF_SEED` 随机消息 250 轮编码/解码/毒化/GC 稳定。

## 失败判读

- `FAIL: ...`：descriptor 解析、presence 或 JSON 编解码语义错误；
- `golden dynamic JSON` 失败：字段顺序/map 排序/空容器输出与约定不符；
- `torture ... input alias`：解码结果引用了编码缓冲区，字符串未复制；
- `SIGSEGV` / `found bad pointer`：descriptor 中的 `reflect.Type`/指针在 GC 后失效或反射字段索引错误；
- 构建错误未拒绝：tag 校验或类型分类存在遗漏。

## 参数

- `UAF_SEED`：随机消息与字段组合种子，失败输出带该值。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_descriptor_dyn_json/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
