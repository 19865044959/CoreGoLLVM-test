---
id: l13_chunkreader_buffered
dim: L13_runtime_ds
priority: P0
sources:
  - pgx@v5.7.4: pgproto3/chunkreader_test.go:9,59 固定窗口缓冲、已缓存数据不重复读底层、Next(0) 重置、随机分片跨块读取
tags: [L13, chunk-reader, fixed-window, compaction, EOF, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 pgx `pgproto3.chunkReader`：固定窗口缓冲 `[rp,wp)`；小请求优先由已缓冲数据满足、耗尽后把剩余数据紧凑到窗口前部再向底层读取；超过窗口的请求用独立大缓冲承接；`Next(0)` 丢弃缓冲并重置读写指针。底层 reader 按随机分片投递数据。GC 插在大块读取返回后与随机读取过程中，验证返回切片与窗口缓冲的存活。

# 期望行为

1. 窗口 32B、底层按 7B 分片：`Next(16)` 恰好触发 3 次底层 Read；随后 `Next(5)` 完全由缓存满足，Read 次数不增加；
2. `Next(0)` 把 rp/wp 归零，之后的 `Next(1)` 强制重新读底层（Read 次数 +1）；
3. 窗口 32B 下连续 4 次 `Next(30)` 跨窗口紧凑，内容逐字节正确；
4. 先 `Next(10)` 再 `Next(200)`（>窗口）得到独立大缓冲，内容正确，且跨 GC 后仍可读；
5. 精确耗尽后 `Next(1)` 返回 `io.EOF`；剩余不足时返回 `io.ErrUnexpectedEOF` 并带回剩余字节；空流返回 `io.EOF`；负长度返回固定错误；
6. `UAF_SEED` 驱动的 4096B 随机分片/随机取块（1..100B）逐字节对照，过程中周期性 GC；
7. 最后一行 `ALL PASS l13_chunkreader_buffered`。

# 失败判读

- 缓存命中时 Read 次数仍增加：窗口判定或 rp/wp 维护错误；
- 跨窗口内容错位：紧凑拷贝的源/目标区间错误；
- 大块读取后 GC 崩溃或内容变化：独立分配的大切片未被正确保活（UAF）；
- EOF 错误类型不对：精确 EOF 与半截 EOF 的区分逻辑错误；
- 内容与底层数据不一致：读取长度或偏移越界。

# 参数

- `UAF_SEED`：随机底层分片大小与随机取块大小的种子，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_chunkreader_buffered/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
