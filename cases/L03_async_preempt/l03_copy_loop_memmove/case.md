---
id: l03_copy_loop_memmove
dim: L03_async_preempt
priority: P2
sources:
  - fasthttp@v1.59.0: server_test.go:3960 请求体流式分段读写，段间插入 GC
  - quic-go@v0.50.1: send_stream_test.go:985 4MB 数据写入/重传后逐字节一致，过程中 GC
tags: [L03, async-preempt, memmove, copy-loop, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

1MiB 的 `[]byte` 源/目的缓冲反复执行 `copy`（memmove），每轮对目的缓冲做全量字节校验和；每 8 轮显式 GC，同时另一 goroutine 持续分配垃圾并 GC。默认 24 轮，可通过 `TORTURE_ROUNDS` 放大。填充字节由 `UAF_SEED` 派生，失败输出带种子。

# 期望行为

1. 复制期间至少一轮背景 GC 完成；
2. 累计复制字节数等于 `rounds * 1MiB`；
3. 每轮复制后的校验和都等于源缓冲参考和（最后一轮记录值用于断言）；
4. 抽样位置字节等于由种子派生的期望值；
5. 进程退出码为 0，最后一行 `ALL PASS l03_copy_loop_memmove`；
6. 同一 `UAF_SEED` + `TORTURE_ROUNDS` 下结果一致。

# 失败判读

- `FAIL: GC completed during copy loop (seed=...)`：GC 未在复制循环期间完成；
- `FAIL: copy byte count (seed=...)`：memmove 被截断或循环提前退出；
- `FAIL: copied data checksum (seed=...)`：复制结果被 GC 破坏（大对象存活/写屏障问题）；
- `FAIL: copied bytes intact after GC (seed=...)`：目的缓冲内容与源不一致；
- 失败信息携带种子，复现时使用同样的 `UAF_SEED`。

# 参数

- `UAF_SEED`：填充字节种子，默认 1，失败输出带出；
- `TORTURE_ROUNDS`：复制轮数（默认 24，每轮 1MiB）；例如 `TORTURE_ROUNDS=1024` 约 1GiB 复制量。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_copy_loop_memmove/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 TORTURE_ROUNDS=1024 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
