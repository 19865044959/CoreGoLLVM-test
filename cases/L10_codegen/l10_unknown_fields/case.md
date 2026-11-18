---
id: l10_unknown_fields
dim: L10_codegen
priority: P2
sources:
  - protobuf-go@v1.36.6: proto/decode_test.go:148 unknown() 通过 SetUnknown 保存未识别字段的原始字节
  - protobuf-go@v1.36.6: proto/merge_test.go:512 merge unknown fields：未知字段按到达顺序追加拼接
  - protobuf-go@v1.36.6: proto/reset_test.go:26/55 Reset 后未知字段被清空
  - protobuf-go@v1.36.6: proto/messageset_test.go:75 MessageSet 对未知字段的保留/过滤路径
  - protobuf-go@v1.36.6: encoding/protowire/wire_test.go:377 TestGroup group 线格式跳过与边界
tags: [L10, L13, unknown-fields, raw-bytes, group, wire-type, merge, reset, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大随机记录轮次"
---

# 场景

生成消息解码器的未知字段保留路径：解码时把未识别字段（含 wire type 与已知字段不匹配、非规范 varint、group）的原始 tag+value 按字段号保存；重编码时已知字段在前、未知字段按字段号升序追加，保证再解码幂等；merge 追加未知片段、reset 清空；输入 buffer 释放或改写后未知字节仍由消息独立持有。对随机混排的已知/未知字段做解码-重编码往返。

## 期望行为

1. 规范顺序的已知字段（varint/string/packed repeated/nested）+ 未知字段解码正确，重编码与输入逐字节一致；
2. 未知字段中的非规范 varint（如 `80 00`）原样保留；同字段号多次出现的片段保持到达顺序；
3. 已知字段 wire type 不匹配时转入未知字段并保留原始字节；
4. 空 packed repeated 不分配；unpacked repeated 逐个追加；嵌套消息内的未知字段保留并在重编码时还原；
5. 未知 group（含嵌套 group）整段保存，结束 group 编号不匹配、未闭合 group、孤立 end-group 均报错；
6. 字段号 0、wire type 6/7、截断的 varint/bytes、截断的 packed varint 均返回错误；空输入是合法空消息；
7. merge：已知标量后者覆盖、repeated 追加、未知片段按来源追加；reset 清空所有字段与未知表；
8. 解码后篡改并释放输入 buffer，多轮 GC 后已知值、未知原始字节与重编码结果不变；
9. `TORTURE_ROUNDS × 60` 条 `UAF_SEED` 随机记录（已知字段与 varint/bytes/fixed32/fixed64/group 未知字段随机混排）解码语义正确，重编码幂等，周期性 GC 后稳定；
10. 最后一行 `ALL PASS l10_unknown_fields`。

## 失败判读

- 重编码丢失未知字段：未知片段未按字段号保存或未追加；
- 未知字段字节被改写：保存了输入切片别名而非拷贝；
- group 解码失败/越界：结束 tag 编号未校验或长度未推进；
- 已知字段被误当归一化：非规范 varint 被重写（未知字段必须原样，已知字段按值处理）；
- merge 后顺序错：同字段号片段未按到达顺序追加；
- 随机失败可凭 seed= 复现；`TORTURE_ROUNDS` 下不稳定说明重编码非幂等。

## 参数

- `UAF_SEED`：随机记录种子，失败输出带 seed；
- `TORTURE_ROUNDS`：随机轮次倍数（默认 1，上限 100）。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_unknown_fields/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 GODEBUG=clobberfree=1 /tmp/case
```
