---
id: l01_generic_map_cache
dim: L01_generics
priority: P1
sources:
  - pgx@v5.7.4: pgtype/pgtype_test.go:527 编码计划缓存按类型 key 共享实例，类型不匹配需回退
  - kitex@v0.13.0: pkg/loadbalance/lbcache/cache_test.go:40 全局 balancerFactories 按 cacheKey 复用同一 factor
  - go-redis@v9.8.0: internal/pool/pool_test.go:16 池 Get/Put 后 Hits/Misses/Idle/Total 统计快照
tags: [L01, generics, map-container, LRU, eviction, hit-miss, pointer-identity, gc, UAF_SEED]
requires: [generics]
runtime: "<10s"
---

# 场景

自建泛型 `Cache[K comparable, V any]`（`map[K]*lruEntry` + 手写双向链表哨兵），模拟 pgx 编码计划缓存与 kitex lbcache 的"按 key 共享实例"语义。用 UAF_SEED 驱动的 LCG 生成 1200 步 Get/Put 序列，同时用独立引用模型（map + 顺序表）逐步比对命中/未命中、旧指针返回、容量淘汰与 Len；每 128 步显式 GC 后按模型全量核对缓存指针与 `blob` 内容。随后删除部分 key、再 GC 复查；最后实例化 `Cache[string,*rec]`，用同一批记录指针验证不同 K 类型实例互不干扰。

# 期望行为

1. 每一步 Get 的 (hit, 指针) 与引用模型完全一致，Hits/Misses/Len 同步递增/淘汰；
2. Put 已存在 key 返回旧指针，新值在后续 Get 中可见；超过容量时最旧项被淘汰且不可见；
3. 每次 GC 后模型内所有 key 的缓存指针仍为原地址，记录字段与 blob 未被破坏；
4. Delete 返回被删指针且后续不可见，GC 后删除项不复活、存活项指针不变；
5. `Cache[string,*rec]` 容量 16，插入 40 个 key 后恰好保留 16 个可见项，指针与 `Cache[int64,*rec]` 共享的记录一致。

# 失败判读

- `... mismatch at step N`：LRU 链表 splice 或淘汰顺序错误，对照 step 与 key 复现；
- `post-GC cache pointer mismatch`：链表/ map 中的 `*V` 未被正确保活，或容器结构被 GC 破坏；
- `pointer mismatch` 但计数正确：map 值被复制成非指针语义，检查泛型参数传递。

# 参数

- `UAF_SEED`：决定容量（48+seed%8）、key 空间与 Get/Put 序列；期望值由模型同步推导，失败输出带种子；
- 无 TORTURE 参数（P1 固定 1200 步）。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_map_cache/*.go
UAF_SEED=7 GODEBUG=clobberfree=1 /tmp/case
```
