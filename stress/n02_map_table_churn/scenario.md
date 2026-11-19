---
id: n02_map_table_churn
priority: P0
covers: [L13, L18, L06]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

map 表翻腾长跑。分片 `map[int64]*node` / `map[string][]*node`：

- 每个分片只由 owner 写（写锁），其他 worker 用读锁并发抽查，形成真实并发读写；
- 每轮删除、重插、`range` 遍历中删除、模型抽样；每 13 轮 `clear` 并重置模型；
- 共享 `sync.Map` 上并发 `Store/Load/Range/Delete`，值内嵌 `*node` 链；
- 持写锁用 `reflect.MapKeys/MapIndex` 抽查两种 map；每 4 轮把抽到的指针带出锁外，插入 `runtime.GC()` 后复核内容；
- NaN key（`math.NaN()` 永不相等 → 每次插入新增条目、查找失败）与 struct key（含 string/array 字段）边界；
- 独立 GC churn + 每 8 轮显式 GC。

来源：`go/src/runtime/map_test.go:253-361`（并发写/遍历/删除）、`go/src/sync/map_test.go:159-219,314-358`（sync.Map 并发）、`go/test/stress/maps.go:32-102`（map 压力形态）。

## 不变量

1. 每个 owner 分片结束时 `map` 的 key 集与写路径模型完全一致（无缺失、无多余），每个值内容满足 `v == key*13+5+idx` 且 tail 一致；
2. 并发读者只能看到“存在且内容正确”或“不存在”，不得读到半写/损坏节点；
3. `sync.Map` 的 Load 返回刚 Store 的同一指针；Range 中每个值都是内容正确的 `*node`；
4. NaN key：插入 4 个 NaN 后 `len==4`、查找不到、遍历 4 次；`clear` 后为空；
5. struct key：相等结构体命中同一项，覆盖不增长长度，delete/clear 行为正确；
6. reflect 抽查的 `MapIndex` 结果有效且内容正确；GC 后仍持有的指针内容不变；
7. 每 worker `started == finished`；固定模式每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；
8. 结束打印 `STRESS PASS n02_map_table_churn`。

## 参数

- `STRESS_SECONDS`（默认 60，0=固定轮数）
- `STRESS_WORKERS`（默认 4；分片数 = workers×2，夹在 [8,64]）
- `STRESS_HEARTBEAT`（默认 5s）
- `UAF_SEED`
- `STRESS_FIXED_ROUNDS`（默认 32，仅 seconds=0）

## 失败判读

- `phase=final ... missing/unexpected key`：map 删除/重插/clear 丢失键或残留键；
- `check-inode/check-strentry/check-syncmap ... corrupted`：存活节点内容被回收或破坏（写屏障/UAF）；
- `phase=nan/structkey`：NaN/struct key 哈希或相等性语义错误；
- `phase=reflect ... invalid`：MapKeys/MapIndex 结果错误或迭代器生命周期问题；
- `phase=syncmap-*`：sync.Map 并发 Store/Load/Range/Delete 丢失或串值；
- `started/finished` 不等：worker 提前退出；
- `goroutine baseline ... leak`：辅助 goroutine 泄漏；
- 只在 clobberfree/efence 下失败：map 值 UAF。

## 运行

```bash
SUITE=stress FILTER=n02 STRESS_SECONDS=60 PROFILES=soak,soak-single ./run.sh
SUITE=stress FILTER=n02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=n02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 PROFILES=base ./run.sh
```
