---
id: l10_thrift_binary
dim: L10_codegen
priority: P2
sources:
  - kitex@v0.13.0: pkg/protocol/bthrift/binary_test.go:336 TestLength 逐类型编码长度
  - kitex@v0.13.0: pkg/protocol/bthrift/binary_test.go:460 TestSkip 混合类型字段编码/按类型跳过
  - protobuf-go@v1.36.6: encoding/protowire/wire_test.go:145 varint 边界、截断与超长拒绝
tags: [L10, L13, thrift-binary, skip, nested-struct, length-table, GC, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大消息轮次"
---

# 场景

模拟 kitex bthrift 生成代码的 Thrift binary 协议编解码：严格版本头（`0x80010000|CALL`）、方法名与 seqid、byte/i16/i32/i64/double/bool/string 字段、list/map/set 容器、递归 struct、未知字段按 wire type 递归 Skip。`Binary.Length` 形态的长度函数与编码结果逐字节核对；解码端拷贝 string/binary，脱离输入缓冲。GC 插在编码与解码之间，并保留编码缓冲在 GC 后重放。

## 期望行为

1. 固定记录（含 MinInt64/MaxInt64、负 int16、Inf、空与非空 binary、有序 map、嵌套 child、全部类型的未知字段）编码成功；前 4 字节为严格版本头，Length 等于实际编码长度；
2. GC 后解码逐字段相等，空（非 nil）binary 仍保留非 nil；
3. 解码结果独立于输入缓冲：整段改写输入后消息不变，GC 后仍不变；
4. 未知字段（struct 内再嵌套 string/list/i16/i64/double/bool/map/set）全部跳过，已知字段完整；带未知字段的编码比无未知字段更长；
5. 任意前缀截断均返回错误且不 panic；坏版本、坏消息类型、坏 bool、负字符串长度、非法 type id、尾部多余字节分别返回对应错误；
6. `UAF_SEED` 随机消息 × 160×`TORTURE_ROUNDS` 轮：长度、roundtrip、GC 后保留缓冲重放全部通过；
7. 最后一行 `ALL PASS l10_thrift_binary`。

## 失败判读

- Length 与实际长度不符：某字段头/容器长度前缀计算错误（生成器长度表 bug 形态）；
- 解码后字段错位：字段 id 或 type 映射错误、Skip 消费字节数不对；
- 改写输入缓冲后消息变化：string/binary 未拷贝，别名到输入缓冲；
- 截断未报错/崩溃：读取前未检查剩余长度；
- GC 后保留缓冲解码失败：编码缓冲未被根保活或解码结果持有输入别名；
- 随机失败可凭 `seed=` 复现。

## 参数

- `UAF_SEED`：随机记录（字段值、容器长度、child、未知字段）种子，失败输出带 seed；
- `TORTURE_ROUNDS`：随机轮次倍数（默认 1，上限 100）。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_thrift_binary/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 GODEBUG=clobberfree=1 /tmp/case
```
