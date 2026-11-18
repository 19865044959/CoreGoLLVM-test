---
id: l05_map_delete_reinsert
dim: L05_alloc_gc
priority: P2
sources:
  - badger@v4.5.0: managed_db_test.go:314 的 TestDropAllRace：持续写入与整表删除/交换并发
tags: [L05, map-churn, delete, reinsert, liveness, gc, torture]
requires: []
runtime: "<10s（默认 2 轮）"
---

# 场景

模拟 key-value 存储的删除/重插压力：2500 个 key 写入 `map[int64]*entry`（entry 含 128 字节 data 与 `*item`），按确定规则分 3 批删除约 60% 的 key，每次删除后 GC，再以更高版本重插；保留被删除 key 的旧引用与第一批重插值跨后续 GC 校验。默认 2 轮，`TORTURE_ROUNDS` 可放大到 20 轮，最终按每 key 的确定版本校验全表。

# 期望行为

1. 初始 2500 个 key 全部存在且版本/内容/CRC 正确；
2. 每批删除后被删 key 立即消失，幸存 key 内容不变；
3. 重插后 map 长度恢复 2500，所有 key 版本等于该轮最后一次重插的版本；
4. 被删除但仍被局部变量引用的旧 entry，经后续删除/重插/多次 GC 后 CRC 与版本不变；
5. 第一批重插值在后续批次 GC 后仍可校验；默认轮数下全绿；
6. 最后一行输出 `ALL PASS l05_map_delete_reinsert`。

# 失败判读

- `still present after delete`：删除语义错误；
- `missing after churn` / `wrong version/content`：重插覆盖遗漏或 map 桶状态被破坏；
- `held deleted entry corrupted` / `pinned reinserted ... corrupted`：仍被引用的 entry 被提前回收（UAF）；
- map 遍历顺序不参与校验，失败定位均为确定 id。

# 参数

- `UAF_SEED`：控制 value 字节模式（默认 1，解析失败回退 1），失败汇总行带出种子；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–20。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_map_delete_reinsert/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=10 /tmp/case
```
