---
id: l02_rwmutex_cache_evict
dim: L02_concurrency
priority: P1
sources:
  - tailscale@v1.80.2: net/dnscache/messagecache_test.go:20 DNS 缓存 TTL 递减、过期 miss、size 上限 2、LRU 淘汰
  - tailscale@v1.80.2: net/flowtrack/flowtrack_test.go:15 五元组 LRU cache，MaxEntries=2，超限淘汰最旧条目
  - etcd@v3.5.0: server/mvcc/watchable_store_test.go:532 多订阅者缓冲受限，慢订阅者触发 victim 淘汰
tags: [L02, RWMutex, cache, LRU, eviction, pinned-entry, concurrent-readers, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 tailscale dnscache / flowtrack 的容量上限与 LRU 淘汰，以及 etcd watch 慢消费者淘汰：自实现 map + 双向链表 LRU，`sync.RWMutex` 保护。

两个阶段：

1. 精确淘汰：容量 4，插入 key 0..3，`GetPromote(0)`、`GetPromote(2)` 后插入 4、5，必须依次淘汰 1、3，最终保留 {0,2,4,5}；
2. 并发读 + 单写者：容量 8，先插入 4 个 pinned hot key（100..103）与 4 个 cold key（200..203）。8 个读者 goroutine 先同时持有 `RLock`（有界自旋等到 active==8，证明读锁可并发进入）后释放；随后 8 个读者各做 500 次 `Get` 只访问 pinned hot key，同时单写者插入 36 个 cold key（300..335）。

hot key 被 pin，淘汰只在 cold 中按 LRU 进行；写者是唯一淘汰者，因此最终保留集合固定为 4 个 hot + 最新 4 个 cold（332..335），淘汰总数 36。读者每 64 次、写者每 8 次插入显式 GC。

# 期望行为

1. 精确阶段淘汰 key 1、3，保留 {0,2,4,5}，淘汰计数 2；
2. 8 个读者能同时持有 `RLock`，`peak_readers==8`，读锁没有互斥化；
3. 并发阶段 4000 次 hot `Get` 全部命中（`hits==4000`、`misses==0`），无 pinned 条目被淘汰；
4. 最终缓存大小 8，淘汰计数 36；
5. cold 300..331 全部被淘汰，cold 332..335 全部保留；
6. hot 条目指针与内容跨多轮显式 GC 不变；
7. 最后一行输出 `ALL PASS l02_rwmutex_cache_evict`，退出码 0。

# 失败判读

- `LRU must evict key N`：LRU 顺序维护错误（链表移动/淘汰端选反）；
- `peak RLock holders=N want=8`：读锁被错误地串行化，或读者屏障死锁；
- `pinned hot key N was evicted`：淘汰扫描未跳过 pinned 条目；
- `old cold key N should be evicted` / `newest cold key N missing`：淘汰顺序错误或淘汰数量不符；
- `hits/misses` 偏差：并发读下条目丢失；
- `hot entry corrupted by GC`：读者在锁外持有的 entry 指针失效或被复用。

# 参数

- `UAF_SEED`：决定 entry 数值；
- 无 `TORTURE_*`；读者迭代与写者插入数固定，淘汰集合可完全判定。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_rwmutex_cache_evict/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GOMAXPROCS=1 GODEBUG=clobberfree=1,asyncpreemptoff=1 /tmp/case
```
