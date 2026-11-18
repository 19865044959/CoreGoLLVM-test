---
id: l05_scratch_reuse_blocks
dim: L05_alloc_gc
priority: P1
sources:
  - klauspost/compress@v1.17.11: huff0/compress_test.go:433 的可复用 scratch 连续处理多块
tags: [L05, scratch-reuse, histogram, slice-reuse, gc]
requires: []
runtime: "<10s"
---

# 场景

模拟 huff0 的可复用 scratch：首块一次性分配输出缓冲与 256 项直方图表，之后连续编码 255 个 128KB 块并复用同一 scratch。编码格式为自写的 RLE（literal/repeat tag），头部带 blockID、CRC32、直方图签名与原始长度；每 8 块显式 GC，每 16 块保留编码副本，随后跨 GC 再解，检测块间串扰。

# 期望行为

1. 首块后 `cap(out)` 固定为 blockSize+256，之后所有块复用同一底层数组（指针相等）；
2. 每个块立即解码后与原始数据逐字节相等，blockID/长度/CRC/直方图签名全部匹配；
3. 每块直方图从零开始统计：若 scratch 表未重置，签名不匹配即失败；
4. 跨多次 GC 后的编码副本仍能正确解出原块内容；
5. 最后一行输出 `ALL PASS l05_scratch_reuse_blocks`。

# 失败判读

- `decode failed`：输出串块、头部损坏或 scratch 表未重置（签名不符）；
- `content mismatch`：RLE 编解码路径错误或输出缓冲被复用改写；
- `scratch out backing array changed` / `cap grew`：scratch 复用失效，每块重新分配；
- 仅 GC 后快照失败：编码副本或 scratch 底层数组被提前回收。

# 参数

- `UAF_SEED`：控制每个块游程数据的内容（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数，固定 255 块。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_scratch_reuse_blocks/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
