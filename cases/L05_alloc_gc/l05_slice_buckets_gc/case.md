---
id: l05_slice_buckets_gc
dim: L05_alloc_gc
priority: P1
sources:
  - klauspost/compress@v1.17.11: huff0/compress_test.go:433 的可复用 scratch 连续处理多块
  - pgx@v5.7.4: internal/iobufpool/iobufpool_test.go:10 的尺寸分桶与缓冲复用
tags: [L05, slice-buckets, segmented-slice, sub-slice, liveness, gc]
requires: []
runtime: "<10s"
---

# 场景

模拟“分桶切片”结构：20 个桶，每桶通过 append 增长 40 个 item 指针，并在桶内
一块连续 `flat` 字节 arena 上为每条记录切子切片保存 data。记录另有独立 tag
指针与 CRC。构建后 GC 校验子切片与桶存储的别名；随后丢弃一半桶，只保留其中
少数 item 跨 GC，再整表丢弃并重建新桶，确认旧 pin 数据不被新桶改写。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_slice_buckets_gc`；
2. 每桶 items 长度为 40，`cap>=len`，flat 使用量等于各记录长度之和；
3. 每条记录的 data 是桶内 flat 的真实子切片，长度/首尾字节/CRC/tag 全部正确；
4. 构建期间每 5 个桶显式 GC 后，已建桶的记录内容不变；
5. 丢弃一半桶并 GC 后，保留 item 的 data 仍通过子切片保活原桶存储；
6. 整表丢弃后仅保留 4 条 item，GC 后仍可校验；
7. 重建的新桶写入 0xaa 后，旧 pin 数据不受影响；
8. 在 base/clobberfree 等 profile 下结果一致。

## 失败判读

- `data does not alias flat` / `alias broken after GC`：子切片未指向桶存储或桶被提前回收；
- `crc=` / `first byte` / `last byte`：桶底层数组被复用改写（UAF）；
- `items cap=...` / `flat cap=...`：append 增长路径异常；
- `dropped-bucket pin+GC` / `final pin`：仅由子切片持有的桶存储未被保活；
- `fresh bucket ... content`：重建路径串写了旧 pin 的底层数组。

## 参数

- `UAF_SEED`：决定每条记录长度与填充字节（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数；固定 20 桶 × 40 条记录。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_slice_buckets_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
