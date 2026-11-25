---
id: u03_gc_assist_pressure
priority: P0
covers: [L05, L18]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

GC assist 压力长跑（P0）。开局预分配一批长寿 anchor 对象图（2^19 节点完全二叉树 + 2^17 节点链 + 稳定 map，注册 finalizer 探针），强引用常驻，占 live heap 大头。随后：

1. **分配风暴**：`STRESS_WORKERS` 个 worker 每轮分配小 `stormNode`、中 `[]uint64`（256~2048 项）与大 `[]byte`（1–4MB）混合临时对象，每个对象带唯一 canary magic；分配后立即校验，再交给独立 verifier goroutine 短暂持有（`holdDepth` 批）后释放，制造“分配–校验–失活”窗口。
2. **GOMEMLIMIT 动态贴近**：governor 每 250ms 用 `runtime.ReadMemStats` 的 `HeapAlloc` 估算 live heap，按 1.0x / 1.1x / 1.2x 轮换调用 `debug.SetMemoryLimit`（开局即 1.0x）。limit 贴近 live 时，分配点会触发 GC assist，mutator 在分配点被拉去标记，栈/寄存器状态半新半旧。
3. **并发写屏障**：独立 worker 持续在老年代 anchor 图上做 old→young 改边（普通指针槽、interface 装箱 `*stormNode`/`[]*stormNode`、map 值），每 8 轮显式 `runtime.GC()` 后仅通过 anchor 图引用读回 young 链并校验存活。
4. **anchor 全量校验**：每个 worker 每 16 轮对整张 anchor 图做一次全量 canary/结构/链/map/动态槽校验，且在显式 GC 前后各一次；收尾恢复 GCPercent/MemoryLimit 原值后再做 GC 前后全量校验。

assist 活跃度**只记录不断言**（Go 1.24 `runtime.MemStats` 无 assist 字段）：metrics 行记录 `NumGC`、`heap_peak`、assist 期分配量（`assist_alloc_bytes`）、`runtime/metrics` 的 `/gc/pauses:seconds` 直方图累计。

## 不变量

1. anchor 树每个节点的 `id/v/canary/tag` 与左右子边身份完全一致；链 `next` 逐节点一致、尾部为 nil；稳定 map 键值身份不变；
2. 动态 old→young 槽位与 map 动态键在每次 GC 后仍指向完整、canary 正确的 young 链（漏写屏障 → 对象被回收/内容损坏 → FAIL）；
3. 临时对象的小/中/大三类 canary 在分配后、交接后、持有期、释放前均校验正确；
4. 预注册 finalizer 的 anchor 可达对象**不得**被终结（终结即 FAIL）；
5. 心跳持续增长（worker/verifier/写屏障 worker 累计），watchdog 6×H 无进展判 FAIL；
6. 收尾 quiesce：恢复 `GCPercent`/`MemoryLimit` 原值、goroutine 回基线、打印 `STRESS PASS u03_gc_assist_pressure`。

## 参数

- `STRESS_SECONDS`（默认 60；0 = 固定轮数）
- `STRESS_WORKERS`（默认 4，上限 32）
- `STRESS_HEARTBEAT`（默认 5s）
- `STRESS_FIXED_ROUNDS`（默认 24，仅 seconds=0）
- `UAF_SEED`
- `STRESS_CRASH_FILE`（runner 注入，经 `debug.SetCrashOutput` 落盘）

## 失败判读

- `anchor-tree/anchor-chain/anchor-map ... corrupted`：anchor 存活对象被回收或内容破坏；
- `old->young ... lost/corrupted after GC`：写屏障/标记漏掉老年代指向年轻代的引用；
- `temp-node/temp-med/temp-big ... canary corrupted`：临时对象在校验窗口内被错误回收/改写（UAF/零化错误）；
- `reachable anchor ... finalized`：可达对象被 finalizer 处理，标记算法错误；
- `watchdog ... no heartbeat`：死锁或 GC 长时间饥饿（注意：limit 贴近 live 时 GC 可能占满 CPU，watchdog 只要求 6×H 周期内至少一次心跳）；
- 只在 clobberfree/efence 下失败：UAF；
- 只在 `-race` 下失败：数据竞争（共享 anchor 动态字段应全部在 `anchorGraph.mu` 保护下）。

## 与 s02 的差异

`s02_heap_cycle_soak` 打的是堆循环与收缩：slab 分配、环形保留、`FreeOSMemory` 收缩/再增长，关注零初始化与存活内容。`u03` 不主动收缩、不跑 GC churn，而是用 GOMEMLIMIT 贴住 live heap 把分配风暴推到 **GC assist 路径**：mutator 在分配点被拉去标记，重点覆盖 assist 期间半新半旧的 mutator 栈/寄存器，以及并发 mark 与 old→young 写屏障的交叠；s02 不覆盖该路径。

## 运行

```bash
SUITE=stress FILTER=u03 STRESS_SECONDS=60 PROFILES=soak ./run.sh
SUITE=stress FILTER=u03 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
SUITE=stress FILTER=u03 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 BUILD_FLAGS=-race PROFILES=base ./run.sh
```
