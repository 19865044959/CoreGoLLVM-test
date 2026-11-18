---
id: l05_goroutine_local_arena
dim: L05_alloc_gc
priority: P2
sources:
  - sonic@v1.13.2: decoder/decoder_test.go:47 的 TestGC：1 万 goroutine 各自解码后调用 runtime.GC
  - badger@v4.5.0: db_test.go:249 的 TestConcurrentWrite：20 个写协程各写 500 条后统一校验
tags: [L05, goroutine-local, arena, ownership-handoff, channel, liveness, gc, torture]
requires: []
runtime: "<10s（默认 2 轮）"
---

# 场景

8 个常驻 worker 各持有一块 64KB 本地 arena。每轮 256 条记录按固定 256B 槽位
切进 arena，记录含 id/name/变长 data 与 next 链；worker 沿链计算摘要，然后把
整块 arena 深拷贝为 handoff、把第 37 条记录深拷贝为 pin，经 channel 交给主
协程。主协程收齐一轮后制造垃圾并两次 `runtime.GC()`，再校验 handoff/pin；
worker 此时在等待下一轮 gate，因此 GC 会扫描其栈上的本地 arena。默认 2 轮，
`TORTURE_ROUNDS` 可放大到 8 轮。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_goroutine_local_arena`；
2. 每轮收齐 8 份结果，无重复、无缺失，worker/round 索引正确；
3. handoff 长度恰为 64KB，每个槽位内容与 worker/round/种子推导值一致；
4. handoff 重算摘要等于 worker 计算的 digest；
5. pin 的 id/name/data 正确且 next 为 nil，深拷贝没有指向 worker 的 recs；
6. 各结果的 handoff/pin 互不别名，改写一个不影响另一个；
7. GC 后所有结果仍完整，worker 在轮间等待时不泄漏/不崩溃。

## 失败判读

- `missing/duplicate result`：worker 未按 gate 产出，或 channel 交接错乱；
- `handoff byte` / `digest mismatch`：本地 arena 复用导致交给主协程的数据被改写（缺少拷贝）；
- `pin still links`：深拷贝不彻底，pin 仍引用 worker 的 slice 数组；
- `alias each other`：多份结果共享了同一底层数组；
- 超时/死锁：gate 广播或结果 channel 容量不足；
- 仅 GC 后失败：goroutine 栈上的 arena/recs 被错误回收。

## 参数

- `UAF_SEED`：决定 arena 每个槽位的字节模式（默认 1，解析失败回退 1），失败汇总行带出种子；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–8；放大后 worker 数与每轮记录数不变。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_goroutine_local_arena/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=6 /tmp/case
```
