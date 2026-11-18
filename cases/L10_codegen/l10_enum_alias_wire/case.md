---
id: l10_enum_alias_wire
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/impl/enum_test.go:15 枚举 Type().New 值与值域映射
  - protobuf-go@v1.36.6: encoding/protojson/encode_test.go:248 未命名枚举数值的 JSON 输出
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:2641 未知枚举名的宽松/严格处理
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:2659 枚举 map 值中的未知数值
  - sqlc@v1.28.0: internal/endtoend/testdata/emit_enum_valid_and_values/go/models.go:55 Valid() 成员语义
tags: [L10, L06, enum, alias, unknown-enum, wire, json, int32-range, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

生成式枚举表与编解码组合：`Color` 含 `COLOR_RED`/`COLOR_CRIMSON` 同值别名，`Level` 含负值、稀疏值与 int32 边界。构建 canonical（首声明胜出）、反查（含别名）、valid 集合与排序后的唯一值表；wire 解码把枚举当 int32 varint，未知数值原样保留并重编码（open enum），负数使用 10 字节符号扩展 varint；JSON 对已知值输出 canonical 名，对未知值输出数字，解码接受别名名与任意 int32 数字，拒绝未知名字、null 与越界数字。未知枚举 map 的键输出排序确定。

## 期望行为

1. 别名表：同值首名获胜、反查接受全部别名、唯一值排序、未知值 `UNKNOWN(n)` 回退；
2. JSON 金标：已知值输出 `"COLOR_RED"`，未知值输出数字（含负数），最小/最大已知值输出名称；
3. JSON 解码接受别名、负数与未知数字，拒绝未知名、null、超出 int32 的数字、错误类型与尾随数据；
4. wire 金标：已知枚举小值一字节 varint；负值 varint 长 10 字节；min/max int32 往返；
5. 未知枚举数值在 wire 往返后顺序与值不变，分类统计 known/unknown/唯一未知值正确；
6. 超出 int32 的 wire 数值（如 1<<32、0xffffffff）被拒绝，已知字段错误 wire type 与截断被拒绝；
7. 未知枚举计数的 JSON map 按数值排序且键名确定；
8. 200 条批量消息与 400 轮 `UAF_SEED` 随机取值（含负值与未知值）在 GC 后仍可稳定往返。

## 失败判读

- `FAIL: ...`：别名 canonical 选择、JSON 名称、未知值保留或 int32 边界处理错误；
- `wire golden`/`negative enum varint length` 失败：枚举编解码长度或符号扩展不一致；
- `SIGSEGV` / `found bad pointer`：枚举表 map/slice 在 GC 后失效（通常为表结构引用错误）；
- 随机轮失败会带 `UAF_SEED`，便于复现具体枚举值组合。

## 参数

- `UAF_SEED`：随机枚举取值种子，失败输出带该值。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_enum_alias_wire/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
