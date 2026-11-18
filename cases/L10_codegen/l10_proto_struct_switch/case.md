---
id: l10_proto_struct_switch
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/encoding/tag/tag_test.go:18 struct tag 驱动编解码
  - protobuf-go@v1.36.6: internal/filedesc/desc_test.go:32 字段描述表构造嵌套消息
  - protobuf-go@v1.36.6: encoding/protowire/wire_test.go:145 varint/截断/超长边界
tags: [L10, generated-struct, field-switch, protowire, zigzag, unknown-field, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 protoc-gen-go 生成消息的编解码形态：生成风格 struct 的 `MarshalAppend`/`Unmarshal` 按 field number 分派到巨大 switch，覆盖 varint/ZigZag、fixed64、bytes、repeated 与嵌套消息；未知字段按 wire type 跳过，非法 wire/截断/超长 varint 全部返回错误。`UAF_SEED` 驱动随机消息树，GC 插在编码与解码之间并保留编码缓冲重放，验证生成代码路径上的指针/切片存活。

## 期望行为

1. 固定消息（负 ID、ZigZag、多 tag、嵌套 child、raw bytes）roundtrip 后逐字段相等；
2. 追加未知字段 9/10 后仍能解码，未知字段被跳过；
3. 空消息编码为 0 字节，解码得到零值；
4. 任意前缀截断输入不 panic，尾部截断返回错误；
5. field 1 用 bytes wire、未知字段用 group wire 均被拒绝；11 字节 varint 被拒绝；
6. repeated 字段保持编码顺序；
7. 300 条 `UAF_SEED` 随机消息 roundtrip 相等，GC 后从保留的编码缓冲重新解码仍正确；
8. 最后一行 `ALL PASS l10_proto_struct_switch`。

## 失败判读

- 固定 roundtrip 失败：字段号/wire type/ZigZag 编解码不对称；
- 随机失败且输出 seed：按 `UAF_SEED` 复现，重点查 child 嵌套长度与 unknown 跳过路径；
- GC 后保留缓冲解码失败：编码结果未逃逸到堆或被错误复用；
- panic/越界：读取前未检查剩余长度；
- 退出码 1：任一 `ck` 失败。

## 参数

- `UAF_SEED`：随机消息树（ID/字符串/tag/child）的种子，失败输出带 seed；
- 无其他放大参数，默认 300 轮。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_proto_struct_switch/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
