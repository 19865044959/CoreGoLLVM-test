---
id: l05_concurrent_compress_gc
dim: L05_alloc_gc
priority: P1
sources:
  - klauspost/compress@v1.17.11: zstd/encoder_test.go:107 的多 goroutine 随机长度切片压缩/解压
tags: [L05, concurrency, compress/gzip, alloc-churn, gc]
requires: []
runtime: "<10s"
---

# 场景

复刻 zstd 并发编码测试的负载形态并替换为 stdlib gzip：6 个 goroutine 从共享 1MB 源中按 `UAF_SEED` 派生随机起点与 16–128KB 长度切片，各自用独立 `gzip.Writer`（每轮 Reset）压缩、`gzip.Reader` 解压；每 3 轮分配垃圾并显式 `runtime.GC()`，主协程在启动前后也强制 GC。

# 期望行为

1. 6 个 worker × 10 轮全部完成，完成轮数等于 60；
2. 每轮压缩流非空，解压后长度、CRC32 与源切片逐字节一致；
3. 并发执行期间的显式 GC 不影响任何往返结果，无 panic/死锁/数据竞争；
4. 在 GOMAXPROCS=1/2 与 GOGC=1 下结果一致；
5. 最后一行输出 `ALL PASS l05_concurrent_compress_gc`。

# 失败判读

- `roundtrip mismatch`：并发压缩下的 Writer 状态串用或缓冲被 GC 破坏；
- `new reader` / `decompress`：压缩流截断或头部损坏；
- `completed rounds` 不足：worker 提前失败或死锁；
- 失败汇总行带 seed；不同 worker/round 的定位信息在 FAIL 行中给出。

# 参数

- `UAF_SEED`：控制共享源内容及每个 worker 的切片起点/长度（默认 1，解析失败回退 1）；
- 无 TORTURE 参数；worker 数与轮数固定，避免 CI 抖动。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_concurrent_compress_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
