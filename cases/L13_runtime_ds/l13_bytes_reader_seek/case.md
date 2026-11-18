---
id: l13_bytes_reader_seek
dim: L13_runtime_ds
priority: P0
sources:
  - sonic@v1.13.2: internal/decoder/api/stream_test.go:70 `NewStreamDecoder(bytes.NewReader(qq))` 跨缓冲边界流式解码
  - goccy/go-json@v0.10.5: decode_test.go:1854 `json.NewDecoder(bytes.NewReader(in))` 以 bytes.Reader 为输入解码
  - goccy/go-json@v0.10.5: benchmarks/decode_test.go:98 bytes.Reader 包裹 fixture 反复读取
tags: [L13, bytes.Reader, seek, readat, writeto, unread, buffer-retention, gc, UAF_SEED]
requires: []
runtime: "<2s"
---

# 场景

模拟流式解码器底层的 `bytes.Reader` 定位语义（payload 约 180KB）：

1. `Size`/`Len` 初值；顺序读 4000B 后 Len 递减；
2. `Seek(12345, Start)` + `ReadByte`/`UnreadByte`/再读；
3. `ReadAt` 绝对读不改变当前 Seek 位置；
4. `Seek(-300, End)` 读尾部，`SeekCurrent` 相对定位；
5. 负偏移 Seek 报错、EOF 处 `ReadByte`/`Read` 返回 `io.EOF`、越界 Seek 后读 EOF、跨尾 `ReadAt` 返回部分字节 + `io.EOF`；
6. 全量顺序读取与 payload 逐字节比较；
7. `WriteTo` 从 offset=777 写出剩余部分，计数与字节摘要匹配，写后 Len=0；
8. GC 后修改底层 payload 首字节，Reader 立即读到新值（验证 Reader 持有切片引用）；恢复后按 `UAF_SEED` 随机 400 次 Seek+ReadByte 校验。

# 期望行为

1. Seek/ReadAt/ReadByte/UnreadByte 的位置与返回值符合 bytes.Reader 语义；
2. EOF 与部分读取的错误/计数正确；
3. 全量读取内容与 payload 完全一致；
4. WriteTo 写出剩余字节、摘要正确并耗尽 Reader；
5. GC 后 Reader 仍引用原 payload，随机定位读取全部正确；
6. 最后一行 `ALL PASS l13_bytes_reader_seek`。

# 失败判读

- `seek/read position` 失败：Seek 基准（Start/Current/End）或 offset 维护错误；
- `ReadAt changed seek position`：绝对读错误地推进了读位置；
- `EOF` 判定失败：边界检查 off-by-one；
- `payload reference lost after GC`：Reader 底层切片未被保活（UAF）；
- `WriteTo digest`：写出起点或消费长度错误。

# 参数

- `UAF_SEED`：payload 内容与随机 Seek 偏移种子，失败输出带该值。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_bytes_reader_seek/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
