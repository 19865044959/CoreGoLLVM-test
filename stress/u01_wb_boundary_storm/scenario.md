---
id: u01_wb_boundary_storm
priority: P0
covers: [L18, L05, L17]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

写屏障极限风暴：把**写屏障实现自身**逼到边界（同一地址高频写、A/B 交替、反向/自环/跨 slab 交错、批量 copy/append、interface 装箱拆箱），与并发 GC mark、GCPercent 抖动叠加；用确定性 canary 让 UAF 当场现形。

与 n04 的分工：n04 打“改边后的对象存活性”（漏标导致的语义后果），u01 打“屏障执行自身的边界与并发”（写路径/缓冲区/并发窗口本身）。两者互补，不重复。

每个 worker 先构造并两轮 `runtime.GC()` 晋升老年代：

- 4096 字段大 struct：1024 组 ×（`*node` / `[]*node` / `map[string]*node` / `interface{}` 四字段）；
- `[]*node` slab 12288 项（`next` 环 + `alt`/`box` 可写槽）；
- 晋升后在一枚 anchor 老对象上注册 finalizer（可达被终结 = FAIL）。

每轮（`youngN=5120` 分区使用）：

1. 分配 5120 个 young node，`v = nodeMagic(seed,id)`，**分配后**全量 canary 校验；
2. 启动并发 `runtime.GC()`（mark 与写重叠，制造 wbBuf flush 竞态窗口），随后：
   - 单地址 2048 次连续写新 young（barrier 缓冲压力）；
   - A/B 交替 1024 次：指针字段与 interface 字段在两个 young 间来回；
   - 跨 slab 交错写 4096 次：old→old 与 old→young 混排（idx 两两不同）；
   - 1024 个 ptr 字段逐字段写，同时 1024 个反向 young→old 写；
   - 1024 个 slice 字段写（新 young backing）、1024 个 interface 装箱写（`*node`/`[]*node` 交替）；
   - 1024 个 map 字段写 + 组 0..7 的 delete/重插；
   - 批量 `copy`/`append` 各 256（目的 backing 均为老年代），与逐字段写混合；
3. **GC 前**校验（canary + 老字段回读 + 拆箱）；
4. 等并发 GC 结束，本轮 young 的本地栈根随函数返回消失，再显式 `runtime.GC()`——此时只有被写屏障正确记录到老对象的 young 才应存活；
5. **GC 后**校验全部 4096 字段 + 跨 slab 抽样 + 反向链 + 批量路径；canary 任何不匹配立即 FAIL；每 8 轮全量校验 slab 环。

独立 churn goroutine 持续分配 + `runtime.GC()`；GCPercent 抖动 goroutine 每 100ms 在 1↔50 之间切换，收尾（阶段边界）恢复原值。所有 node 的 `v` 字段 = 确定性 magic，三段校验构成 UAF 探针。

来源：`go/src/runtime/gc_test.go`（特别是写屏障/并发标记压力与 `clobberfree` UAF 检测）、`go/src/runtime/mbarrier.go`（wbBuf flush）、n04 的 old→young 图思路（本场景仅复用晋升骨架，不重复其“存活性”断言）。

## 不变量

1. 每个 node 的 `v == nodeMagic(seed,id)`（分配后 / GC 前 / GC 后三段校验），任何不匹配立即 FAIL；
2. GC 后全部 1024 个 `*node` 字段、1024 个 slice 字段、1024 个 interface 字段、1024 个 map 字段仍指向本轮内容正确的 young 对象（漏标 → 回收 → canary 失配）；
3. 同一地址终值、A/B 终值、自环 `old.alt == old` / `old.box.(*node) == old`、反向链、跨 slab 抽样、批量 copy/append 内容均符合确定性写序列；
4. slab `next` 环恰好 12288 项且逐项内容正确；
5. 可达 anchor 的 finalizer 从不执行（`anchor_finalized == 0`）；
6. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started == finished`；
7. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
8. 最后一行 `STRESS PASS u01_wb_boundary_storm`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤32） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 随机/ID 种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 32 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `canary mismatch ... v=... want=...`：对象已被回收/复用——写屏障漏标或 UAF（重点看 `phase=post-gc|final` 与字段名；`soak`/`clobberfree` 下最敏感）；
- `id=... want=...`：指针指向了错误对象（写序列错乱/并发写丢失）；
- `nil *node` / `slice len=... want=1` / `interface not ...`：老字段被清空、容器/装箱路径损坏或 young 被回收；
- `slab[...] ring broken`：老年代 slab 结构被破坏；
- `reachable anchor was finalized`：可达性/finalizer 语义错误；
- `no worker progress`：watchdog 超时（写或 GC 卡死），附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- `hulk fields=... want=4096`：场景自身结构退化（仅 preflight）；
- 只在 clobberfree/soak 下失败：UAF/漏标；base 也失败多为写序列逻辑错误。

## 运行

```bash
SUITE=stress FILTER=u01 STRESS_SECONDS=60 PROFILES=soak,soak-single ./run.sh
SUITE=stress FILTER=u01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=u01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 PROFILES=base ./run.sh
```
