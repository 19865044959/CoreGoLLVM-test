---
id: l05_stream_large_value_pooled
dim: L05_alloc_gc
priority: P1
sources:
  - badger@v4.5.0: stream_writer_test.go:582 的大 value 经池化缓冲写入后读回
tags: [L05, sync.Pool, large-value, streaming, crc32, gc]
requires: []
runtime: "<10s"
---

# 场景

模拟 badger stream writer 的大 value 路径（规模缩小到 3MB/值）：3 个 3MB value 经 1MB `sync.Pool` 缓冲分块写入内存 sink，每写完一个 value 显式 GC；随后在再次 GC 之后，经池化缓冲分块读回，校验整值 CRC 与逐字节内容。

# 期望行为

1. 每次借出的池缓冲 `len==0` 且 `cap>=1MB`；
2. 3 个大 value 的 sink 偏移连续，总长恰为 9MB；
3. 写入路径的整值 CRC 与读回路径重算 CRC 相等；
4. 读回时每 1MB 分块与 `valueByte(v,i,seed)` 逐字节一致，长度精确；
5. 池缓冲跨 GC 写入标记后内容仍完整；
6. 最后一行输出 `ALL PASS l05_stream_large_value_pooled`。

# 失败判读

- `pooled ... buffer shape`：池对象长度未清零或容量错误；
- `byte mismatch at=N`：分块边界错位、缓冲复用串块；
- `crc got=... want=...`：大值被截断或部分分块损坏；
- `sink length`：写入路径少写/多写分块；
- GC 后集中失败：sink 或池缓冲底层数组被提前回收。

# 参数

- `UAF_SEED`：控制大 value 的字节模式（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数，固定 3×3MB。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_stream_large_value_pooled/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
