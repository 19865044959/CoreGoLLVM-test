---
id: l03_divmod_index_loop
dim: L03_async_preempt
priority: P0
sources:
  - nats-server@v2.11.0: server/ring_test.go:22 容量 10 的环形缓冲 append 30 次，覆写旧元素并校验 len/total
  - etcd@v3.5.0: raft/tracker/inflights_test.go:22 环形 buffer 的 Add/FreeTo/FreeFirstOne，覆盖回绕边界
tags: [L03, async-preempt, division, modulo, computed-index, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

长循环每轮先做 `i/div` 与 `i%div`（`div` 由 `UAF_SEED` 决定，运行时变量，不能常量折叠），再由商、余数、乘数与偏移算出 `% mod` 后的节点下标并读取 `*dvNode`。`mod` 也由种子决定（4093/4094），故意不是 2 的幂。每 32768 轮分配垃圾并显式 `runtime.GC()`，GC pump 并发执行 8 轮 GC。

# 期望行为

1. 并发 GC pump 至少完成一轮；
2. 循环至少执行 131072 次；
3. 累加和等于按同一除/模/下标公式、只做整数推算的参考值；
4. GC 后全部节点字段不变；
5. 进程退出码为 0，最后一行 `ALL PASS l03_divmod_index_loop`；
6. 同一 `UAF_SEED` 下结果一致。

# 失败判读

- `FAIL: GC pump completed during div/mod loop`：计算索引的长循环期间 GC 未完成；
- `FAIL: div/mod index checksum (seed=...)`：除法/取模或下标计算被破坏，或节点指针读取错误；
- `FAIL: div/mod nodes intact after GC (seed=...)`：节点被回收或字段被改写；
- 超时：循环与 GC 互锁。

# 参数

- `UAF_SEED`：决定除数、乘数、模数与偏移，默认 1，失败输出带出；
- 迭代下限 131072、显式 GC 周期 32768 均为固定常量。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_divmod_index_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=8 GOGC=1 GODEBUG=clobberfree=1,asyncpreemptoff=0 /tmp/case
```
