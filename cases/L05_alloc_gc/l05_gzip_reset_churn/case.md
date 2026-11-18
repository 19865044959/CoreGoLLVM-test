---
id: l05_gzip_reset_churn
dim: L05_alloc_gc
priority: P1
sources:
  - klauspost/compress@v1.17.11: gzip/gzip_test.go:226 的 Writer.Reset 循环复用与递减载荷
tags: [L05, compress/gzip, Writer.Reset, buffer-reuse, gc]
requires: []
runtime: "<10s"
---

# 场景

用 stdlib `compress/gzip` 复刻 klauspost gzip 的 Reset 循环：同一个 Writer 经 `Reset` 反复写 20 轮递减载荷（256KB 起每轮减 8KB），每轮输出独立缓存；轮间分配垃圾并 `runtime.GC()`。全部轮次结束后统一解压，校验 Reset 没有复用脏窗口/哈希表/写缓冲导致输出损坏。

# 期望行为

1. 每轮 `Reset` 后 Write 与 Close 均无错误，压缩输出非空；
2. 每轮输出在后续 Reset 与 GC 后不被改写；
3. 20 份输出逐一解压后长度与原载荷一致、CRC32 一致；
4. 最后再 Reset 一次写 `tail-after-reset`，可完整往返；
5. 最后一行输出 `ALL PASS l05_gzip_reset_churn`。

# 失败判读

- `round=N decompress ...` 或 `checksum mismatch`：Reset 未清空内部状态，输出串轮；
- `compressed too short`：Write/Close 状态机错误；
- 仅 clobberfree/stress 下失败：Writer 内部缓冲被提前回收或未重新初始化；
- `tail roundtrip`：多次 Reset 后 Writer 永久损坏。

# 参数

- `UAF_SEED`：控制载荷的游程内容（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数，固定 20 轮。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_gzip_reset_churn/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
