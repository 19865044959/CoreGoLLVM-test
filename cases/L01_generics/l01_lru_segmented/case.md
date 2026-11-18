---
id: l01_lru_segmented
dim: L01_generics
priority: P1
sources:
  - go-redis@v9.8.0: internal/pool/pool_test.go:16 Get/Put 后 Hits/Misses/Idle/Total 统计快照
  - pgx@v5.7.4: pgtype/pgtype_test.go:527 编码计划缓存按类型 key 共享实例，类型不匹配需回退
  - kitex@v0.13.0: pkg/loadbalance/lbcache/cache_test.go:40 全局 balancerFactories 按 cacheKey 复用同一 factor
tags: [L01, generics, LRU, SLRU, segmented-cache, eviction, reference-model, pointer-identity, gc, UAF_SEED]
requires: [generics]
runtime: "<10s"
---

# 场景

自建泛型分段 LRU `SLRU[K comparable, V any]`：`map[K]*segEntry` + probation/protected 两段手写双向链表。新条目进入 probation；命中 probation 时晋升 protected，protected 满（容量一半）时把最旧条目降级回 probation；淘汰只发生在 probation 尾部。该结构对应真实缓存中"热点稳定、冷数据快速淘汰"的编码计划缓存与共享 factor 缓存。

用 UAF_SEED 驱动的 LCG 生成 900–1000 步 Get/Put 序列，同时用独立顺序表引用模型逐步比对命中/未命中、旧指针返回、Len/ProtectedLen/ProbationLen 与统计计数；每 96 步显式 GC 后按 key 全量 `Peek` 核对指针身份与 blob。随后删除每 7 个 key 中的一个并 GC 复查，最后实例化 `SLRU[string, rec]` 验证不同 K 类型实例互不干扰。与 `l01_generic_map_cache` 的单段 LRU 相比，本例额外覆盖晋升/降级与段容量约束。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_lru_segmented`；
2. 每一步 Get 的命中结果与返回指针、Put 的旧指针/存在性与引用模型完全一致；
3. 每一步 `Len == len(model.m)`、`ProtectedLen == len(model.prot)`、`ProbationLen == len(model.prob)`，Hits/Misses 同步；
4. 显式 GC 后所有存活 key 的 `Peek` 指针不变，`id/tag/blob` 未被破坏；
5. 删除后存活 key 指针不变、被删 key 不可见；string 键实例的容量、命中计数与指针身份正确；
6. 在 clobberfree 等 profile 下结果一致。

# 失败判读

- `FAIL: ...pointer mismatch` / `payload corrupted`：缓存节点或负载被错误回收/覆盖，优先怀疑泛型实例化后的指针位图或写屏障；
- `protected/probation len` 或 hit/miss 分叉：淘汰/晋升逻辑或编译器对泛型方法的内联优化结果异常；
- `SIGSEGV` / `fatal error: found bad pointer`：大概率栈映射漏标或写屏障缺失；
- 超时：链表成环或 GC 饥饿。

## 参数

- `UAF_SEED`：随机操作序列种子，同时决定记录数、容量与 key 空间；
- 无 TORTURE 参数（P1 固定规模）。

## 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_lru_segmented/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
