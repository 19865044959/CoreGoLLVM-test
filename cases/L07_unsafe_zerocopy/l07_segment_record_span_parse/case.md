---
id: l07_segment_record_span_parse
dim: L07_unsafe_zerocopy
priority: P2
sources:
  - protobuf-go@v1.36.6: internal/impl/lazy_buffersharing_test.go:91 lazy 消息与输入 buffer 共享，不必要拷贝不得发生
  - klauspost-compress@v1.17.11: s2/writer_test.go:579 BufferDone 按 10B..8MB 档位归还缓冲，验证所有权与复用契约
  - pgx@v5.7.4: pgproto3/chunkreader_test.go:9 已缓冲数据按偏移读取，耗尽后重置
tags: [L07, unsafe, segmented, cross-boundary, zero-copy, string-header, liveness]
requires: []
runtime: "<10s（TORTURE_ROUNDS 可放大）"
---

# 场景

分段零拷贝的跨段边界：大 payload 按 64KiB 分段，记录（3 字节长度前缀 + 变长记录体）
顺序铺在缓冲上。生成器把每个段边界前的记录放到 `nextB-4`，使 8 字节记录体跨过段边界，
另有记录起点恰在边界上。解析后每条记录体用 `unsafe.String` 直接引用输入，记录描述只保留
偏移、长度与 FNV-1a 校验和；同时保留段视图用于核对记录首/末字节与相邻段的一致性。
模拟 protobuf lazy buffer sharing、压缩库分档缓冲与协议解码器跨读取块的零拷贝视图。

## 期望行为

1. 记录数 >64，且每个内部段边界都被至少一条记录体跨过；
2. 每条记录体视图的 `{data,len}` 指向输入对应偏移，FNV 校验和等于按输入模式独立重算值；
3. 每个段边界字节同时出现在记录视图与相邻两个段视图中，值一致；
4. 改写跨段记录体内位于段边界处的字节，视图立即观察到变化；恢复后一致；
5. 整块 payload 校验和与独立重算一致；
6. 丢弃 payload/段视图变量并显式 GC 后，所有轮次保留的记录视图仍可读且校验和不变；
7. `TORTURE_ROUNDS` 放大轮次（默认 2，上限 8）不出现悬挂视图；
8. 末行 `ALL PASS l07_segment_record_span_parse`，失败退出 1。

## 失败判读

- 记录视图内容与模式不符：长度前缀偏移或 `unsafe.String` 长度计算错误；
- 段边界未被覆盖：生成器布局逻辑错误（用例自身问题）；
- GC 后读记录崩溃/efence 报错：记录 string 头未被当作根，或大对象扫描漏标；
- 改写不可见：记录视图发生了复制，零拷贝语义丢失；
- 仅 k8s/stress 失败：GOGC=1 下大对象存活/标记问题。

## 参数

- `UAF_SEED`：决定 payload 大小（512KiB/576KiB/640KiB）与字节模式，失败信息带种子；
- `TORTURE_ROUNDS`：轮次放大（默认 2，钳制到 1..8），每轮保留一组记录视图。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_segment_record_span_parse/*.go
UAF_SEED=1 TORTURE_ROUNDS=4 GODEBUG=clobberfree=1 /tmp/case
```
