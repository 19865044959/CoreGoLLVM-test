---
id: l13_overlap_compact_ranges
dim: L13_runtime_ds
priority: P1
sources:
  - badger@v4.5.0: db_test.go:447 TestStreamDB 全量流式复制/压缩后逐 key 校验
  - badger@v4.5.0: value_test.go:207 TestValueGC 100×32KB 大值删除 45 个后重写并校验剩余
  - goccy/go-json@v0.10.5: json_test.go:197 TestCompactBig 大 JSON 原地压缩并逐字节往返
tags: [L13, copy, overlap, compaction, record-buffer, pointer-slice, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 LSM/值日志的原地记录压缩：

1. 构造 600 条记录（2B id + 2B len + payload，约 20KB），依次执行 4 轮删除：稀疏谓词、连续区间 [120,240)、前缀 [0,50)、后缀 [>=520)；每轮用 `copy(buf[w:w+n], buf[r:r+n])` 原地前移，w<r 时源/目的重叠；
2. 每轮与独立重建的参考字节缓冲逐字节比较，解析记录并校验 id/payload，GC 后再解析；
3. 右移重叠：在前部插入 9 字节头，`copy(dst>src)` 搬移主体，校验 body 与 header 跨 GC 不变；
4. `[]*crev` 指针切片按连续 run 用重叠 copy 压缩 4 轮，尾部置 nil，GC 后逐项校验指针身份与 payload；
5. 对已压缩缓冲执行“无删除”压缩，必须幂等。

`UAF_SEED` 决定 payload 与第一轮稀疏删除谓词。

# 期望行为

1. 每轮压缩后保留 id 集合、缓冲内容与参考完全一致，GC 后仍一致；
2. 连续区间删除后大段重叠前移不破坏记录边界；
3. 右移重叠插入头后主体逐字节不变；
4. 指针切片压缩后顺序、身份、payload 正确，尾部已清零；
5. 无删除压缩不改内容且保留计数正确；
6. 最后一行 `ALL PASS l13_overlap_compact_ranges`。

# 失败判读

- `bad record` / `payload corrupted`：重叠 copy 方向或长度错误、记录边界被破坏；
- `buffer mismatch`：原地压缩结果与参考不一致；
- `identity bad` / `after GC bad`：指针切片搬运漏写屏障或尾部未清零；
- `right-shift overlap changed body`：dst>src 的重叠搬移错误；
- 超时：压缩循环未推进。

# 参数

- `UAF_SEED`：payload 与删除谓词种子，失败信息附 seed。

# 运行

```bash
go build -o /tmp/l13_overlap_compact_ranges ./cases/L13_runtime_ds/l13_overlap_compact_ranges/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/l13_overlap_compact_ranges
```
