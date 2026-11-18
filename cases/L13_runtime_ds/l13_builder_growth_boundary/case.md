---
id: l13_builder_growth_boundary
dim: L13_runtime_ds
priority: P1
sources:
  - go-redis@v9.8.0: internal/proto/writer_test.go:93 BenchmarkWriteBuffer_Append 反复 Append 参数的扩容基准
  - goccy/go-json@v0.10.5: stream_test.go:529 TestIssue278 长字符串尾部带转义、贴近缓冲边界
  - sonic@v1.13.2: encoder/encoder_test.go:113 TestEncodeErrorAndScratchBuf cap=10 小缓冲写错误路径
tags: [L13, strings.Builder, growth, WriteRune, Reset, clone, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

`strings.Builder` 的增长边界与复用：

1. 360 步混合 `Write`/`WriteString`/`WriteByte`/`WriteRune`/`Grow`，chunk 长度覆盖 1、63/64/65、511/512/513、4095/4096/4097、8191/8192/8193、65535/65536/65537，与 `[]byte` 参考逐段比对；
2. 每 45 步校验 Len/内容，并显式 GC 后再校验；
3. 50 轮 Reset 复用：Reset 后 Len==0/String==""，重写内容正确，跨 GC 保持；
4. 克隆语义：目标保留已有前缀后 `WriteString(src.String())`，源 Reset 并重写后目标内容不变；
5. 10,000 个多字节 `WriteRune`：输出必须合法 UTF-8 且 rune 数精确，GC 后不变；
6. 90 块 64KB 附近大块追加（约 5.9MB），每 30 块 GC 并比对全部字节。

`UAF_SEED` 决定 chunk 内容、rune 起始偏移与 Grow 尺寸。

# 期望行为

1. 任意增长边界下 Builder 内容与参考字节序列逐字节一致；
2. GC 前后内容、Len 不变；
3. Reset 完全清空且可立即复用；
4. 克隆结果独立于源后续 Reset/重写；
5. WriteRune 路径输出合法 UTF-8、rune 计数为 10000；
6. 大块追加完成后总长度与内容精确；
7. 最后一行 `ALL PASS l13_builder_growth_boundary`。

# 失败判读

- `builder: content mismatch`：Builder 扩容复制丢字节或越界写；
- `builder reset:`：Reset 未清长度/缓冲复用错误；
- `builder clone:`：String/WriteString 拷贝语义错误；
- `builder runes:`：WriteRune 编码或边界处理错误；
- `SIGSEGV`：Builder 内部 slice 越界或 GC 后缓冲失效。

# 参数

- `UAF_SEED`：内容与尺寸序列种子，失败信息附 seed。

# 运行

```bash
go build -o /tmp/l13_builder_growth_boundary ./cases/L13_runtime_ds/l13_builder_growth_boundary/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/l13_builder_growth_boundary
```
