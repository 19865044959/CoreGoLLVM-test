---
id: l13_bytes_buffer_reset_reuse
dim: L13_runtime_ds
priority: P1
sources:
  - badger@v4.5.0: backup_test.go:205 同一 `bytes.Buffer` 先 Backup，`backup.Reset()` 后再次 Backup 并校验长度
  - pgx@v5.7.4: tracelog/tracelog_test.go:130 `buf.Reset()` 清理建立连接期间写入的日志
  - go-redis@v9.8.0: internal/proto/writer_test.go:93 BenchmarkWriteBuffer_Append 反复复用同一缓冲写参数
tags: [L13, bytes.Buffer, reset, reuse, grow, truncate, readfrom, writeto, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟协议编码/日志缓冲的 Reset 复用：

1. 写入 40 段变长数据，保存快照与内容，Reset 后 Len/Bytes 归零，快照不被 Reset 改写；重写后内容正确，GC 前后一致；
2. 同一 `bytes.Buffer` 连续 40 轮 Reset/重写，容量单调不降且 `cap>=len`，每 10 轮 GC 后内容逐字节校验；
3. `Grow(64KB)` 预留后写入 100KB，`Truncate(40000)` 再写 60000B，验证截断/覆盖结果与 GC 稳定性；
4. 读写交错：写出 1000B、读 300B、再写 2000B，验证内部偏移压缩后剩余数据顺序正确；
5. `ReadFrom(strings.NewReader(50000B))` 与 `WriteTo(digestWriter)`：计数、摘要、清空状态正确；
6. `ReadByte`/`UnreadByte`/后续 `Read` 的位置语义；
7. 200KB 数据驻留 Buffer 跨 GC 后逐字节一致，Reset 归零。

`UAF_SEED` 决定各段长度与字节内容。

# 期望行为

1. Reset 后长度/内容清零且可立即复用，旧快照独立；
2. 复用循环容量不下降，内容与参考逐轮一致；
3. Grow/Truncate/重写的内容、长度正确；
4. 读写交错压缩后剩余字节序正确；
5. ReadFrom/WriteTo 的字节数与摘要与源一致，WriteTo 后缓冲为空；
6. GC 前后所有驻留内容不变；
7. 最后一行 `ALL PASS l13_bytes_buffer_reset_reuse`。

# 失败判读

- `content mismatch/mutated`：Reset 未清零或内部偏移/容量维护错误；
- `Truncate/rewrite` 失败：截断后写位置或旧数据覆盖错误；
- `interleaved` 失败：读后写未正确压缩未读区间；
- `WriteTo digest` 失败：消费位置或底层切片长度错误；
- GC 后内容乱码：Buffer 底层数组未被正确保活（UAF）。

# 参数

- `UAF_SEED`：段长与内容种子，失败输出带该值。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_bytes_buffer_reset_reuse/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
