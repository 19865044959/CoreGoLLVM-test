---
id: l10_defaults_decode
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/encoding/defval/default_test.go:17 默认值按 kind 格式化（bool/int/float/string/bytes 转义/枚举）
  - ent@v0.14.4: schema/field/field_test.go:44 Default(10)+Min/Max 生成的默认值描述
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:184 JSON null 保持字段未设置，读取时走默认
  - protobuf-go@v1.36.6: proto/equal_test.go:464 显式设置默认值与未设置值在相等性上的区分
tags: [L10, L16, defaults, decode-time, presence, bytes-escaping, merge, reset, table-build, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

proto2 风格默认值的解码期应用：默认值表由紧凑 spec 构建（bool/int32/int64/double/hex string/hex bytes/enum），解码时记录字段 presence；缺席字段读取时返回默认值但 `Has=false`，显式零值、空字符串、空 bytes 保持 present 且不被默认覆盖；嵌套消息缺席保持 nil，其 payload 出现时应用消息内默认值；repeated/map 无默认值；merge/reset 与默认表字节隔离。

## 期望行为

1. 空消息解码后所有默认值可读（bool true、int32 -7、int64 1<<40、double π、含 NUL 的 UTF-8 字符串、6 字节 bytes、enum=2），但 Has 全为 false；默认值不写回线格式；
2. 显式 present 的零值/空串/空 bytes/零枚举保持 present，读取值为显式值；空 bytes 解码后非 nil；
3. 显式非零值与未知枚举号原样保留，不被默认表覆盖；
4. 嵌套消息缺席为 nil；空 payload 使其出现且内部默认 n=42（hasN=false）；显式 n=0/7 时 hasN=true；
5. repeated/map 无默认值，缺席为 nil；present 元素正常解码；merge 时 repeated 追加、map 键覆盖、已有 presence 字段不被覆盖，再次 merge 空消息不改值；
6. 非 merge 解码先重置再应用默认；reset 后值为零、Has=false，再次应用默认表恢复默认值且幂等；
7. 默认 bytes 与消息/Get 副本隔离：修改 Get 结果或消息字段不改变默认表，reset 后重新取默认值仍为原始字节；
8. 默认表构建校验：重复字段号、未知 kind、非法字段号、int32 溢出、非法 float/hex/奇数 hex、未定义枚举、缺字段均报错；表按字段号排序；
9. 解码错误：截断 varint/bytes/child、已知字段 wire type 错误、字段号 0、wire type 6 均报错；
10. 输入 buffer 改写/释放并多轮 GC 后显式值、默认值与默认表不变；
11. 200 条 `UAF_SEED` 随机 presence 组合（含嵌套消息、repeated/map）与默认值矩阵逐项一致，周期性 GC 后重编码一致；
12. 最后一行 `ALL PASS l10_defaults_decode`。

## 失败判读

- 默认值覆盖显式零值：presence 位未记录或应用顺序错误；
- 空 bytes 变 nil：解码用 `append(nil, ...)` 而非 make/copy；
- 嵌套消息被自动实例化：解码器为缺失 message 字段创建对象；
- merge 把默认值写回：merge 后无条件应用默认表；
- Get 修改污染默认表：默认 bytes 未复制；
- 默认值被序列化：编码器按"是否等于零值"判断而非 presence；
- 表构建接受非法 spec：缺少数值/枚举校验；
- GC 后乱码：消息或默认表未保活，输入 buffer 别名；
- 随机失败可凭 seed= 复现。

## 参数

- `UAF_SEED`：随机 presence/值组合种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_defaults_decode/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
