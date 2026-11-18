---
id: l10_oneof_roundtrip
dim: L10_codegen
priority: P0
sources:
  - protobuf-go@v1.36.6: proto/oneof_get_test.go:25 TestOpenGet Open 生成消息 oneof Get/Has 方法集
  - protobuf-go@v1.36.6: proto/oneof_get_test.go:150 TestOpaqueGet Opaque oneof 访问器与零值语义
  - protobuf-go@v1.36.6: encoding/protowire/wire_test.go:145 varint 边界与截断拒绝
tags: [L10, L06, oneof, marshal, roundtrip, typed-nil, last-one-wins, unknown-field, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 protoc-gen-go 的 oneof 生成形态：`Which isOneof` 接口 + 16 个包装类型（string/int32/int64/bool/bytes/fixed64/嵌套消息/uint32/uint64/enum/packed repeated/tag/data/fixed32/count/note），字段号与 wire type 表固定。`MarshalAppend` 只编码当前分支，typed-nil 包装视为未设置；解码按字段号恢复分支并在同组多字段时 last-one-wins，未知字段按 wire type 跳过。覆盖字节级 golden、空 blob/空 child presence、截断与非法输入、追加缓冲复用和 GC 保活。

## 期望行为

1. 16 个分支逐一 marshal → GC → unmarshal 后类型与值一致，非当前分支 getter 全为零；
2. 字节级 golden 正确：text `0a 02 hi`、bool `20 01`、空 blob `2a 00`、int32(-1) 的 10 字节符号扩展、float64 fixed64、packed list 长度；
3. typed-nil 分支编码 0 字节、WhichField=0、getter 返回零且不 panic；
4. 空（非 nil）blob 解码后仍为 presence 且长度 0；空 child 解码后非 nil；
5. 同字段/跨分支重复出现时后值生效；未知字段 99/100 被跳过；
6. 截断的 text/blob/nested child 前缀全部报错；错误 wire、bool 值 2、11 字节 varint、group wire 分别拒绝；
7. `MarshalAppend` 保留 `HDR` 前缀，40 次复用底层数组且 GC 后从复用缓冲解码正确；
8. 500 条 `UAF_SEED` 随机分支（含未设置/typed-nil/零值/随机值）roundtrip 相等；GC 后保留的四元组（源、编码、重编码、重解码）全部一致；
9. 最后一行 `ALL PASS l10_oneof_roundtrip`。

## 失败判读

- 分支串值：类型 switch 顺序或接口装箱错误；
- typed-nil panic 或误判：未判空包装指针；
- last-one-wins 失败：解码未用后值覆盖前值；
- 空 blob/child 变 nil：解码走 `append(nil,...)` 或零长度未分配；
- golden 不符：字段号/wire type/端序/ZigZag 编码不对称；
- GC 后读取乱码/崩溃：oneof 载荷切片或 child 未保活；
- 随机失败可凭 `seed=` 复现。

## 参数

- `UAF_SEED`：随机分支/取值种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_oneof_roundtrip/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
