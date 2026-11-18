---
id: l10_marshal_buffer
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: proto/methods_test.go:45 Legacy Marshal/Unmarshal/Size 快速路径一致
  - protobuf-go@v1.36.6: encoding/protowire/wire_test.go:145 varint 边界与截断拒绝
  - grpc-go@v1.71.1: rpc_util_test.go:351 消息大小限制/错误路径与 buffer 复用
tags: [L10, marshal, append-buffer, packed-repeated, size, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟生成代码的 `Size`/`Marshal`/`MarshalAppend`/`Unmarshal`：消息含普通字段、packed repeated int64（ZigZag）、bytes 与递归 child。`MarshalAppend` 支持在已有前缀（"HDR"）后追加并复用底层数组；`Unmarshal` 必须拷贝字符串/字节载荷，脱离输入缓冲。GC 插在编码与解码之间、以及复用循环中。

## 期望行为

1. 空消息 Size=0 且编码 0 字节；nil 消息 Marshal 返回错误、Size=0；
2. 固定消息 Size 等于编码长度，GC 后解码逐字段相等；
3. 解码后把源缓冲整段改写，消息内容不变；GC 后仍不变；
4. `MarshalAppend` 保留 3 字节前缀，长度 = 3+Size；50 次复用不破坏前缀，且每次 GC 后从复用缓冲解码正确；
5. 截断尾部、错误 wire 报错；超过 64 层嵌套返回深度错误；空（非 nil）blob 解码后仍为非 nil 空切片；
6. 250 条 `UAF_SEED` 随机消息：Size 与编码长度一致、roundtrip 相等，GC 后从保留缓冲重放正确；
7. 最后一行 `ALL PASS l10_marshal_buffer`。

## 失败判读

- Size 与编码长度不一致：字段省略规则或 varint 长度计算错误；
- 改写源缓冲后消息变化：Unmarshal 未拷贝 string/[]byte（别名到输入缓冲）；
- 复用循环解码失败：MarshalAppend 未从 `dst` 尾部继续写，或前缀被覆盖；
- 深度错误未触发：递归解码缺少上限；
- GC 后失败：编码缓冲/子消息未被保活。

## 参数

- `UAF_SEED`：随机记录树（字段/Values/blob/child）种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_marshal_buffer/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
