---
id: l01_generic_ttl_lru
dim: L01_generics
priority: P1
sources:
  - gorm@v1.30.0: tests/lru_test.go:397 TestLoadingExpired 过期后 Peek/Get 的可见性差异
  - gorm@v1.30.0: tests/lru_test.go:34 TestLRU_Add_ExceedsSize_RemovesOldest 容量超限淘汰最旧项
  - grpc-go@v1.71.1: internal/cache/timeoutCache_test.go:37 带 TTL 的 cache 到期删除与 Remove/Clear 语义
  - tailscale@v1.80.2: net/dnscache/messagecache_test.go:20 TTL 递减、过期 miss、LRU 容量上限
tags: [L01, generics, TTL, LRU, container-list, fake-clock, eviction, hit-miss, pointer-identity, gc, UAF_SEED]
requires: [generics]
runtime: "<10s"
---

# 场景

自建泛型 `ttlCache[K comparable, V any]`：`map[K]*list.Element` + `container/list` 手写 LRU，条目携带 `expireAt`，使用注入的假时钟（`Advance`）而非真实时间，保证确定性。Get 命中续热但不续期；过期项在 Get/Purge 时删除，Peek 只读且不改变顺序与统计。对应 gorm `internal/lru` 的 TTL/容量淘汰与 grpc-go timeoutCache 的到期行为。

UAF_SEED 驱动 600 步 Set/Get/Peek/Advance/Purge/Delete 序列，容量 8+seed%7，并与独立参考模型逐步比对命中/未命中、指针、Len、LRU 顺序与 Hits/Misses。每 61 步显式 GC 后全量 Peek 核对存活指针与 blob；最后删除部分 key 并把假时钟推进 1000，验证 Purge 清空且无复活。另实例化 `ttlCache[int64,*int64]` 覆盖容量淘汰与更新时间后重新到期。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_ttl_lru`；
2. 每一步 Get 的 (命中, 指针)、Peek 可见性、Len、front→back key 顺序与参考模型一致；
3. 每次 Set 后超容量时恰好淘汰最旧项，已存在 key 被更新且移到队首；
4. 假时钟推进到 `expireAt` 后 Get 返回 miss 并删除，Peek 不可见但保留到 Purge；
5. Purge 返回的删除数与模型一致，删除后 Len/顺序同步；
6. Hits/Misses 每一步与模型一致；
7. 显式 GC 后所有未过期条目的指针身份、blob 内容与 LRU 顺序不变；最终推进时钟后缓存清空且已删 key 不复活。

# 失败判读

- `LRU order mismatch` / `len mismatch`：MoveToFront 或淘汰端（Back）用错；
- `get/peek pointer mismatch`：条目值被复制或过期路径误删存活项；
- `expired entry visible`：比较符方向错误（now >= expireAt 才是过期）；
- `post-GC payload corrupted`：缓存未正确保活负载，偏 L18；
- `SIGSEGV` / `fatal error: found bad pointer`：栈映射漏标或写屏障缺失；
- 超时：链表成环（MoveToFront/Remove 配对错误）。

# 参数

- `UAF_SEED`：决定容量、操作序列、TTL 与 key 空间；失败输出带种子；
- 无 TORTURE 参数（P1 固定 600 步，单次 <1s）。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_ttl_lru/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
