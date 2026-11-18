---
id: l05_gomemlimit_pressure
dim: L05_alloc_gc
priority: P2
sources:
  - klauspost/compress@v1.17.11: zstd/decoder_test.go:222 的 TestNewDecoderMemory：5000 个 Decoder 实例各读 16KB 并统计堆占用
  - badger@v4.5.0: db_test.go:2066 的 TestSyncForRace：ticker 并发 Sync 与 128KB 大值写入，穿插 GC
tags: [L05, GOMEMLIMIT, memory-pressure, heap-live-set, gc, torture]
requires: []
runtime: "<10s（默认 2 轮）"
---

# 场景

在 runner 可能注入的 `GOMEMLIMIT`（如 k8s profile 的 256MiB）下做分配压力：
每轮保持数百个 ~8KB live 对象跨显式 GC，同时持续分配 48 × 256KB 短命垃圾，
再释放一半 live 集并 GC。用例只读取 `os.Getenv("GOMEMLIMIT")` 用于调整常驻
规模，绝不调用 `debug.SetMemoryLimit` 或改写环境变量。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_gomemlimit_pressure`；
2. 每轮 live 对象的 id/label/data/next 环在填表后与 GC 后均与种子推导值一致；
3. 大块垃圾 + 两次显式 GC 后，仍存活的 live 对象内容不变；
4. 释放一半 live 对象后，保留项仍完整且计数正确；
5. 全部轮次结束后，pin 的对象仍有效；
6. `TotalAlloc` 增长、`HeapAlloc>0`、`NumGC` 至少增加 `2 × 轮数`；
7. 运行前后 `GOMEMLIMIT` 环境变量文本值完全相同；
8. 在 base/clobberfree/efence/stress/k8s 等 profile 下结果一致（k8s 下自动缩小 live 集）。

## 失败判读

- `live ... corrupted`：GC 压力下对象被提前回收或内容被复用；
- `TotalAlloc/NumGC` 断言失败：分配/GC 路径未实际执行；
- `GOMEMLIMIT env changed`：用例意外设置了内存限制（违反只读约束）；
- OOM/超时：live 集或垃圾规模超出 profile 上限。

## 参数

- `UAF_SEED`：决定 live 对象长度与字节内容；失败输出带种子；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–6；
- 环境变量 `GOMEMLIMIT` 只读，不设置；由 runner 的 profile 决定是否存在。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_gomemlimit_pressure/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOMEMLIMIT=256MiB GOGC=1 UAF_SEED=1 TORTURE_ROUNDS=6 /tmp/case
```
