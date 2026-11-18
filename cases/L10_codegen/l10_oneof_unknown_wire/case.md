---
id: l10_oneof_unknown_wire
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: proto/oneof_get_test.go:25 Open 生成消息 oneof Get/Has 方法集
  - protobuf-go@v1.36.6: proto/merge_test.go:500 Merge oneof 标量字段
  - protobuf-go@v1.36.6: proto/decode_test.go:110 typed-nil oneof 包装解码
  - protobuf-go@v1.36.6: proto/decode_test.go:148 unknown() 保存未识别字段原始字节
  - protobuf-go@v1.36.6: proto/merge_test.go:512 Merge 未知字段追加
tags: [L10, L06, oneof, unknown-fields, wire, typed-nil, last-one-wins, merge, clear, group, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

oneof wire 编解码与未知字段保留的组合：6 个分支（text/num/blob/node/flag/fixed32 count），解码时同组多个已知字段 last-one-wins；未知字段号（含 group、fixed32/64、bytes、varint）以及**已知字段号但 wire type 不匹配**的字段，都按原始 tag+value 保存并在重编码时追加，且不改变/清空当前已选分支。typed-nil 包装视为未设置；ClearChoice 只清分支、ClearUnknown 只清未知、Reset 全清；merge 时分支深拷贝覆盖、typed-nil 不覆盖、未知字段按顺序追加。

## 期望行为

1. 6 个分支分别编码→解码后类型与值一致；空 text/blob、false、count=0 仍表示“已选择”；
2. typed-nil `*chNode`：Has=false、编码为空、merge 时不覆盖目标分支；
3. 金标：text "hi" + 未知字段 7 varint 5 的字节序列正确；
4. 已知分支 last-wins，被覆盖分支的 getter 为零；
5. 混合未知字段（含 group）按到达顺序逐字节保留，输入 buffer 毒化 + GC 后仍一致；
6. 已知字段号配错 wire type：不选分支、不清空已有分支、可稳定往返；
7. merge：分支替换且 blob/node 深拷贝，未知字段追加；nil 源 no-op；
8. Clear/Reset 的独立清理语义正确；
9. 9 类截断、非法 wire、未闭合 group 被拒绝；
10. `UAF_SEED` 300 轮随机分支 + 未知字段混合，毒化/GC 后状态与重编码稳定。

## 失败判读

- `FAIL: ...`：oneof 分支选择、typed-nil 或未知字段保留语义错误；
- `wrong-wire field does not clear branch` 失败：wire type 不匹配被误当成同组字段处理；
- `unknown fields re-encode identically` 失败：未知字段被丢弃/重排/重新编码；
- `buffer alias`：解码结果引用输入缓冲区；
- `SIGSEGV` / `found bad pointer`：分支或 group 原始字节复制时漏拷。

## 参数

- `UAF_SEED`：随机分支与未知字段组合种子，失败输出带该值。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_oneof_unknown_wire/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
