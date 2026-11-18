---
id: l10_fixed_size_codec
dim: L10_codegen
priority: P2
sources:
  - protobuf-go@v1.36.6: encoding/protowire/wire_test.go:305 TestFixed32 fixed32 值/EOF/截断边界
  - protobuf-go@v1.36.6: encoding/protowire/wire_test.go:327 TestFixed64 fixed64 值/EOF/截断边界
  - kitex@v0.13.0: pkg/protocol/bthrift/binary_test.go:336 TestLength 逐类型编码长度表
tags: [L10, L13, fixed-size, fixed32, fixed64, endian, bitfield, crc, truncation, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大随机记录轮次"
---

# 场景

生成风格固定大小编解码：64 字节 little-endian 记录（magic、版本、位标志、u64/i64/f64、16 字节定长 ID、保留区、CRC32）与 32 字节 big-endian 小记录（magic、u32 字段、8 字节 ID、保留区、CRC16-CCITT）。编码写固定偏移并计算校验；解码校验长度、magic、版本、未知标志位、保留区零值与 CRC，ID 按值拷出输入缓冲。

## 期望行为

1. 两个布局的 golden 十六进制逐字节正确，`Length()` 分别为 64/32；
2. 固定记录与极值记录（MaxUint64/MinInt64/NaN/±Inf）往返一致，NaN 按位模式比较；小记录往返一致；
3. 任何 0..63 截断、66 字节、坏 magic、版本 0/4、未知 flags、保留区非零（已修 CRC）、坏 CRC 均被拒绝；编码非法版本/非法 flags/nil receiver 返回错误；小记录截断/保留区/CRC 同样拒绝；
4. 解码结果独立于输入缓冲，整段改写输入后字段不变；
5. 保留编码缓冲与解码记录经多轮 GC 后仍可正确解码，定长 ID 数组不变；
6. `150×TORTURE_ROUNDS` 个随机大记录与 `100×TORTURE_ROUNDS` 个小记录：往返字段（含 NaN 位）、重编码字节确定、周期性 GC 后解码一致；
7. 最后一行 `ALL PASS l10_fixed_size_codec`。

## 失败判读

- golden 不符：字段偏移/端序/保留区/CRC 计算错误；
- 截断未报错：读取前未检查长度；
- 保留区/未知 flags 接受：前向兼容校验缺失；
- 改输入后记录变化：ID 或有效载荷别名到输入缓冲；
- GC 后解码失败：编码缓冲未保活或栈对象逃逸问题；
- 随机失败可凭 `seed=` 复现；
- `TORTURE_ROUNDS` 下不稳定：随机位模式/GC 保留错误。

## 参数

- `UAF_SEED`：随机记录字段/ID/位模式种子，失败输出带 seed；
- `TORTURE_ROUNDS`：随机轮次倍数（默认 1，上限 100）。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_fixed_size_codec/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 GODEBUG=clobberfree=1 /tmp/case
```
