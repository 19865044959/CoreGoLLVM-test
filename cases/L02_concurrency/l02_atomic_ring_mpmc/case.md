---
id: l02_atomic_ring_mpmc
dim: L02_concurrency
priority: P2
sources:
  - grpc-go@v1.71.1: internal/profiling/buffer/buffer_test.go:106 固定容量环形缓冲，双 goroutine 并发 Push/Drain 不丢不重
  - quic-go@v0.50.1: internal/utils/ringbuffer/ringbuffer_test.go:9 环缓冲 Push/Peek/Pop、回绕与 Clear
  - etcd@v3.5.0: raft/tracker/inflights_test.go:22 环形 buffer Add/FreeTo/FreeFirstOne，覆盖 start 回绕与满窗
tags: [L02, atomic, ring-buffer, mpmc, sequence-counter, lock-free, checksum, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 4×120 项；TORTURE_ROUNDS 放大每生产者条数，上限 16 倍）"
---

# 场景

模拟 quic-go / grpc-go 的固定容量环形缓冲，但使用原子序号实现 MPMC 环：容量 16，每个槽位有 `atomic.Uint64 seq`，生产者 CAS `tail` 后发布序号，消费者 CAS `head` 后归还槽位（Vyukov 有界队列结构）。

默认 4 个生产者各 push 120 条带 `*item` payload 的消息，3 个消费者并发 pop。每个生产者维护自己的期望值，消费者用按 `prod*per+seq` 索引的 atomic 位图去重；每条消息逐生产者校验 payload 并与期望校验和比对。`enq`/`deq` 原子计数必须都等于总数。生产/消费循环每 40/64 条显式分配垃圾并 `runtime.GC()`，并跨 GC 校验当前 payload。结束条件由 `prodDone + enq==total + deq==total + consumed==total` 共同判定，不依赖时间。

# 期望行为

1. 所有 `4×120` 条消息恰好入队一次，`enq==total`；
2. 所有消息恰好出队并处理一次，`deq==total`、`consumed==total`，位图无缺口、无重复；
3. 每个生产者的 payload 校验和与其期望和相等；
4. 消费者观察到的每条消息 `prod/seq/v/tag` 自洽；
5. 显式 GC 后正在传递的 payload 内容不变；
6. `TORTURE_ROUNDS` 放大条数后上述不变量仍成立；
7. 最后一行输出 `ALL PASS l02_atomic_ring_mpmc`，退出码 0。

# 失败判读

- `consumed=N want=total` / `enqueued` / `dequeued`：原子序号发布或 CAS 推进丢失/重复；
- `duplicate item from ring`：消费者 CAS 竞争下同一槽位被取两次；
- `missing items=N`：槽位被覆盖或消费者提前退出；
- `producer N checksum mismatch`：payload 内容损坏或消息错配；
- `consumer observed invalid item`：槽位 payload 指针在 GC 后被复用/破坏；
- 超时：生产者在满环上永久自旋，或消费者在空环但生产已结束时未退出。

# 参数

- `UAF_SEED`：决定 payload 数值与逐生产者期望和；
- `TORTURE_ROUNDS`：每生产者条数 = `120 × rounds`（默认 1，上限 16），环容量固定 16。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_atomic_ring_mpmc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=16 GODEBUG=clobberfree=1 /tmp/case
```
