---
id: l02_lru_weighted_bytes
dim: L02_concurrency
priority: P2
sources:
  - fiber@v2.52.6: middleware/cache/cache_test.go:712 MaxBytes=2 时按插入/访问顺序 LRU 淘汰
  - gorm@v1.30.0: tests/lru_test.go:350 1000 goroutine 并发 Add 100 个 key，最终 Len()==100
  - nats-server@v2.11.0: server/filestore_test.go:6363 PurgeEx 后消息块 cache 只保留一个热块，防止批量清理把全部块读入内存
tags: [L02, LRU, weighted-capacity, eviction-order, container/list, concurrent-cache, eviction-accounting, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 8×300 次操作；TORTURE_ROUNDS 放大操作数，上限 8）"
---

# 场景

模拟按字节配额淘汰的并发 LRU：map + container/list + mutex，超配额从队尾淘汰，记录每个被淘汰载体；单条超过总配额的条目必须拒绝且不改变缓存。对应 fiber 的 MaxBytes LRU、gorm 并发 LRU 与 nats 块缓存只保留热块。

## 期望行为

1. 进程退出码为 0，最后一行 `ALL PASS l02_lru_weighted_bytes`；
2. 串行阶段：30+40+50 超配额先淘汰最久未用的 30；访问提升 40 后插入 25 淘汰 50；最终总量 65；101 字节条目被拒绝且状态不变；
3. 并发阶段：任意时刻总字节 ≤ 200；map 与 list 长度一致；每个 live/evicted 载体载荷在 GC 前后有效；
4. 记账恒等：`insertedNew == evictions + live + rejected`；淘汰确实发生；
5. 最终总量与淘汰记录数自洽。

## 失败判读

- `total exceeds capacity`：淘汰循环边界或更新已有 key 的重量差计算错误；
- `map/list length mismatch`：淘汰时只删 map 或只删 list；
- `insert accounting mismatch`：淘汰/新增计数遗漏，或更新被误计为新增；
- `recency promotion failed`：Get 未移动到队首，淘汰顺序错误；
- `live/evicted entries invalid`：载荷被更新覆盖或 GC 破坏。

## 参数

- `UAF_SEED`：决定载荷数值与字节内容，失败信息带种子；
- `TORTURE_ROUNDS`：放大每 worker 操作数（默认 1，上限 8）；容量固定 200 字节、8 worker、key 空间 40。

## 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_lru_weighted_bytes/*.go
UAF_SEED=1 TORTURE_ROUNDS=4 GODEBUG=clobberfree=1 /tmp/case
```
