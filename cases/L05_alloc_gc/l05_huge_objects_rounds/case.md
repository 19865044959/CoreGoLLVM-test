---
id: l05_huge_objects_rounds
dim: L05_alloc_gc
priority: P2
sources:
  - klauspost/compress@v1.17.11: gzip/gzip_test.go:408/417 的 1MB/10MB 大缓冲往返
  - badger@v4.5.0: stream_writer_test.go:582 的 10MB 大 value 经大缓冲写入
tags: [L05, huge-object, large-allocation, alloc-free-rounds, crc32, gc, torture]
requires: []
runtime: "<10s（默认 2 轮）"
---

# 场景

每轮依次分配 1/2/4/8MB 四个超大对象并写满确定模式，其间穿插垃圾分配；四个对象存活时显式 GC 并校验边界字节与 CRC，随后为每个尺寸保留一份副本、释放原对象、分配约 4MB 垃圾并两次 GC，校验副本；再整批重建一次并 GC。默认 2 轮，`TORTURE_ROUNDS` 可放大到 8 轮，验证大对象独立 span 的分配/释放/保活。

# 期望行为

1. 四个尺寸的分配长度精确为 1/2/4/8MB，边界与 1MB 步进采样字节符合 `hugeByte`；
2. 存活期间 GC 后全部对象 CRC 与生成时一致；
3. 原对象释放 + 两次 GC 后，每尺寸保留副本的长度/CRC/内容仍正确；
4. 整批重建并 GC 后，本轮所有 pin 副本与重建对象同时有效；
5. 最终 pin 数量为 `4×轮数`，全部 CRC 正确；
6. 最后一行输出 `ALL PASS l05_huge_objects_rounds`。

# 失败判读

- `corrupted while alive after GC`：大对象在存活期被回收或 span 元数据错误；
- `pinned ... after release+GC`：已释放大对象的副本引用了被复用内存（UAF）；
- `rebuilt corrupted`：大对象复用已回收页后内容错误；
- efence 下 OOM/超时：`TORTURE_ROUNDS` 过大，收回到默认规模。

# 参数

- `UAF_SEED`：控制填充字节基值（默认 1，解析失败回退 1），失败汇总行带出种子；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–8（10 轮约 140MB RSS）。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_huge_objects_rounds/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=6 /tmp/case
```
