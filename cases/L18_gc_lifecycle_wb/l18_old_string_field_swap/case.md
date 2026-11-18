---
id: l18_old_string_field_swap
dim: L18_gc_lifecycle_wb
priority: P0
sources:
  - pgx@v5.7.4: pgtype/bytea_test.go:49（行扫描复用同一缓冲，旧 string/[]byte 结果被后续行覆盖）
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1466（messageState 缓存的 interface 指针跨 Reset/GC 必须保留）
tags: [L18, write-barrier, old-to-young, string, byte-slice, interface, noinline, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 8 轮；TORTURE_ROUNDS 上限 32）"
---

# 场景

24 节点老环形链表已整体晋升。每轮 noinline `ssRewire` 遍历环，用 `ssSetNode`
把新分配的年轻 string、[]byte 与 `*ssTag` 写入老节点的 `name/blob/tag` 字段；
按 `UAF_SEED` 谓词，约 1/5 节点同时把这三类字段清空。每次写入后立即双 GC，
再从 head 沿环全量校验 24 个节点的内容与零化状态。

与既有 slice/struct 批量改写用例不同，这里只针对 string 头、字节切片头与
interface 头三类指针字段的写屏障，并混合“写入-清空-再写入”的边界。

## 期望行为

1. 未清空节点的 string 内容、blob 长度与逐字节数据、interface 动态类型与内容全部正确；
2. 被清空节点的 name 为空串、blob/tag 为 nil，无旧代残值；
3. 环形 next 指针跨全部轮次保持完整，节点 id 顺序不变；
4. 失败信息带 `seed=<UAF_SEED>`；
5. 退出码 0，最后一行 `ALL PASS l18_old_string_field_swap`。

## 失败判读

- `name corrupted`：string 头写入丢失或底层字节被提前回收；
- `blob corrupted`：[]byte 写屏障或长度字段损坏；
- `interface tag corrupted`：interface/itab 写入或对象存活判定错误；
- `not cleared`：字段清空未生效，旧指针残留会被后续 GC 误当根。

## 参数

- `UAF_SEED`：清空谓词、字符串长度/字符与 blob 数据模式；
- `TORTURE_ROUNDS`：轮数（默认 8，上限 32）。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_old_string_field_swap/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=24 UAF_SEED=5 /tmp/case
```
