---
id: l05_map_small_objects
dim: L05_alloc_gc
priority: P2
sources:
  - sonic@v1.13.2: issue_test/issue100_test.go:30 的 TestLargeMapValue：大 struct 作为 map value 由运行时指针存储
  - sonic@v1.13.2: issue_test/issue762_test.go:15 的 TestIssue762：20 goroutine × 5000 次解码小对象
  - pgx@v5.7.4: pgtype/hstore_test.go:266 的 TestRoundTrip：map 文本/二进制往返，含 nil/空 map
tags: [L05, map-churn, small-objects, pointer-fields, liveness, gc, torture]
requires: []
runtime: "<10s（默认 2 轮）"
---

# 场景

模拟 KV/文档解码后产生的“大 map + 大量独立小对象”结构：每个 map value 是含
string、[]byte、peer 指针和共享 tag 的独立堆对象。表建成后建立对象间 peer 环，
强制 GC，再覆盖 1/3、删除 1/7 的项，最后整表丢弃并制造垃圾，检验 map bucket
扫描、对象存活与旧值不被复用。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_map_small_objects`；
2. 建表后 `len(map)==12000`，每个对象的 id/name/weight/data/tag 与种子推导值一致；
3. 显式 GC 后未修改项、peer 指针目标与 pin 住的被覆盖旧对象内容全部完整；
4. 删除的 key 确实不存在，覆盖后的项是新一轮对象且 link 指向共享 anchor；
5. 整表置空并分配大块垃圾再 GC 后，pin 对象仍完整；
6. 在 base/clobberfree/efence/k8s 等 profile 下结果一致。

## 失败判读

- `FAIL: round=... id=... corrupted`：map value 指向的小对象内容被改写，优先怀疑堆扫描/写屏障或对象复用；
- `link corrupted`：对象间指针被提前回收或指向错误对象；
- `deleted key still present` / `len mismatch`：map 删除/覆盖逻辑或哈希实现异常；
- 崩溃或 `fatal error: found bad pointer`：map bucket 中的指针位图/写屏障问题。

## 参数

- `UAF_SEED`：决定 weight、data 长度与字节内容；失败输出带种子；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–8，放大后每轮重建整张表。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_map_small_objects/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_ROUNDS=8 /tmp/case
```
