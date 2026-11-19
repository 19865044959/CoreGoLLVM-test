---
id: n04_write_barrier_graph
priority: P0
covers: [L18, L05, L17]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

写屏障/老年代对象图长跑。每个 worker 先构造并按两轮 `runtime.GC()` 晋升：

- 完全二叉树 4095 节点（left/right 指针）；
- 1024 节点链（next 指针）；
- `map[int64]*gnode` 256 项；
- `[]*gnode` slab 4096 项；
- 预分配批量 copy 的 src/dst `[]*gnode` 各 1024。

每轮：分配年轻链（64 节点，young→old 边）；在老节点上写 old→young 普通指针字段、interface 装箱（`*gnode` 与 `[]*gnode`）、map 值；交换 depth-1 内部节点的左右子树；`copy(dst, src)` / `append` 批量写 `[]*gnode`；执行删除+重插。每 2 轮显式 `runtime.GC()` 并校验 young 链在 GC 后仍可达；每 8 轮全量校验对象图。独立 GC churn goroutine 并发施压。

来源：`go/src/runtime/gc_test.go:42-124,623-718`（GC/写屏障并发压力）、<https://github.com/dotnet/runtime/blob/main/docs/design/coreclr/jit/investigate-stress.md>（GC 高频插入思路）。

## 不变量

1. 树从根可达且恰好 4095 个节点，无重复/环/多父；
2. 每个节点 `v == id*3+1`、`tag=="n04"`；内部节点（id < 2047）度=2 且子节点集合为 `{2i+1, 2i+2}`，叶子度=0（旋转只换左右，不改集合）；
3. 节点 id 校验和 = 4095×4094/2；
4. 链恰好 1024 节点、内容/顺序正确；map 恰好 256 键且值内容正确；slab 4096 项均非 nil 且内容正确；
5. GC 后 old→young 边仍保活 young 链：`anchor.link`、`anchor.box.(*gnode)`、`other.box.([]*gnode)`、map 值、批量 copy 目标全部指向内容正确的 young 节点；
6. 固定模式每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；每 worker `started == finished`；
7. 结束打印 `STRESS PASS n04_write_barrier_graph`。

## 参数

- `STRESS_SECONDS`（默认 60，0=固定轮数）
- `STRESS_WORKERS`（默认 4）
- `STRESS_HEARTBEAT`（默认 5s）
- `UAF_SEED`
- `STRESS_FIXED_ROUNDS`（默认 32，仅 seconds=0）

## 失败判读

- `tree node ... content corrupted`：老年代节点内容被破坏（过早回收/写屏障漏标）；
- `duplicate tree node / reachable tree nodes / checksum`：树连通性被破坏（改边/旋转错误或对象被回收）；
- `degree / child set`：父子关系被破坏；
- `old->young link lost` / `interface-boxed ... lost` / `map ... value lost`：old→young 写屏障漏标，young 对象被错误回收；
- `bulk-copy ... dst mismatch/corrupted`：`[]*node` 批量 copy 的 bulk write barrier 漏标；
- `chain[...] / slab[...] corrupted`：存活对象内容损坏；
- `started/finished` 不等或轮次不足：worker 提前退出；
- `goroutine baseline ... leak`：辅助 goroutine 泄漏；
- 只在 clobberfree/efence 下失败：UAF；`gccheckmark=1` 夜间模式可放大漏标。

## 运行

```bash
SUITE=stress FILTER=n04 STRESS_SECONDS=60 PROFILES=soak,soak-single ./run.sh
SUITE=stress FILTER=n04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=n04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 PROFILES=base ./run.sh
```
