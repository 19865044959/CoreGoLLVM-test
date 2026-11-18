---
id: l02_syncmap_pointer_cache
dim: L02_concurrency
priority: P2
sources:
  - tailscale@v1.80.2: syncs/syncs_test.go:167 泛型 Map 的 Load/Store/LoadOrStore 并发语义与单赢家判定
  - nats-server@v2.11.0: server/sublist_test.go:957 不可变快照发布后读侧无锁，GC 与高并发读交叉
  - grpc-go@v1.71.1: resolver/map_test.go:74 AddressMap 同键不重复增长，覆盖等价键
tags: [L02, sync.Map, pointer-cache, LoadOrStore, Range, generation, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 writers×192 次；TORTURE_ROUNDS 放大写/读轮次）"
---

# 场景

模拟 tailscale 泛型并发 Map 与 nats-server 订阅快照缓存：`sync.Map` 保存不可变 `*entry`（key、generation、seed 派生 tag、固定 probe 数组）。

1. 主键区间 0..63 由 4 个写者分区独占：每个写者在自己的 16 个 key 上反复 `Load` 校验后 `Store` 新代，读者 3 路并发 `Load` 任意 key 并校验对象自洽；
2. 独立 key 区间 1000..1015 由 8 个 goroutine 用 `LoadOrStore` 竞争插入，每个 key 恰好一个赢家；
3. 写者每 29 次、读者每 37 次显式分配垃圾并 `runtime.GC()`，持有点指针跨 GC 再校验；结束后 `Range` 全量统计 key 数与代数总和。

# 期望行为

1. 任意 `Load` 得到的 entry 满足 `gen` 在 `[0, ops/16]`、`tag == f(seed,key,gen)`、probe 与 gen 的固定关系；
2. 64 个主键每个最终代数恰为 `ops/16`，全量 `Range` 得到 80 个 key，代数总和确定；
3. `LoadOrStore` 竞争恰好产生 16 个赢家、`8×16-16` 个 loaded=true，且每个 race key 的最终值为合法 entry；
4. 所有跨 GC 持有的 entry 内容不变；
5. 最后一行 `ALL PASS l02_syncmap_pointer_cache`。

# 失败判读

- `writer/reader loaded invalid entry`：指针缓存对象被 GC 破坏或写者发布了半初始化对象；
- `LoadOrStore winners/losers`：sync.Map 竞争插入语义异常（多于一个赢家或丢赢家）；
- `cache size` / `generation sum`：Range 漏键、重复键或代数计算错误；
- `entry corrupted after GC`：指针跨 GC 失效，重点排查 stack map / 写屏障。

# 参数

- `UAF_SEED`：决定 entry tag 的派生值（不参与 key 调度），失败输出带种子；
- `TORTURE_ROUNDS`：放大 ops 与读者轮次，范围 1..100，默认 1。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_syncmap_pointer_cache/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=20 GODEBUG=clobberfree=1 /tmp/case
```
