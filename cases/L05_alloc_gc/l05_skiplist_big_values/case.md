---
id: l05_skiplist_big_values
dim: L05_alloc_gc
priority: P2
sources:
  - badger@v4.5.0: skl/skl_test.go:162 的多 goroutine 大 value 并发写入与读取
tags: [L05, skiplist, concurrency, large-object, gc, torture]
requires: []
runtime: "<10s"
---

# 场景

将 badger skiplist 的 100×1MB 并发写读缩小为默认 64 个 256KB 大对象：前半 key 串行预填后，4 个写协程并发插入后半 key，4 个读协程同时按种子随机读取前半 key 并校验 CRC；主协程在并发期间持续分配垃圾并显式 `runtime.GC()`。结束后校验有序遍历与全部 64 个 key 的内容。

# 期望行为

1. 并发写读期间无 nil 值、无 CRC 不一致，读写由 RWMutex 保护且无数据竞争；
2. 并发阶段结束后 skiplist 恰好包含 64 个 key（放大时按比例），有序遍历严格递增；
3. 每个 key 的值的长度、CRC 与生成模式一致；
4. 显式 GC 与垃圾分配贯穿并发写入阶段，所有存活大对象内容不变；
5. 最后一行输出 `ALL PASS l05_skiplist_big_values`。

# 失败判读

- `concurrent read key=N ... nil/checksum`：节点在并发插入/读取中被破坏或大 value 被提前回收；
- `order broken`：跳表层级指针损坏；
- `key=N missing/len/checksum after concurrent phase`：插入丢失或 value 底层数组 UAF；
- 仅 stress/k8s 下失败：GC 与写屏障路径问题，而非并发协议问题。

# 参数

- `UAF_SEED`：控制 value 内容与跳表层级随机源（默认 1，解析失败回退 1），失败汇总行带出种子；
- `TORTURE_SCALE`：对象数与对象大小倍数，默认 1，范围 1–4（4 时约 256 个 1MB 对象）；
- `TORTURE_ROUNDS`：重建并校验的轮数，默认 1，范围 1–20。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_skiplist_big_values/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_SCALE=2 TORTURE_ROUNDS=2 /tmp/case
```
