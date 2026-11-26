---
id: g04_escaped_result_heapaddr
priority: P1
covers: [L18, L17]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

堆逃逸结果的 heapaddr 槽：issue2 的**变体**——对准 §4.7 定位的修复缺口与 §5.1 的 CoreGo 对应路径（liveness/plive.go `Needzero`：堆逃逸结果 heapaddr 槽入口清零，防 malloc 触发的栈扫撞垃圾）。

- **结果本身堆逃逸**：`victimEscape() (res BigInfo)` 的命名结果被取址 `&res` 经 `resultHolder` 接口送走（被 holder 持有）——结果物化到堆，调用方帧上是 heapaddr 槽形态（gollvm 的 `alloc->heap_` 路径，cd24507a 的 `!heap_` 分支跳过清零）；
- **返回前 GC 扫 heapaddr 槽**：victim 函数体内先 `h.hold(&res)` 建立逃逸，随后 `runtime.GC()`（返回前）——heapaddr 槽未初始化窗口被扫描（陈旧堆地址被当活指针 = 缺陷触发点）；
- **脏堆预热**：每轮先 `warmupDirtyHeap` 分配大量同 size class 短命 `BigInfo` 后 GC（堆里布满刚释放的 span），再调 victim——陈旧槽值更可能命中已释放 span → `pointer ... to unallocated span` / `found pointer to free object` 形态；
- **双路写后校验**：返回值 copy + 跨 GC 后经 holder 读回的堆对象，均全字段 canary 校验；weak 强引用断言兜底。

官方 Go：结果对象由分配器零化 + `Needzero` 入口清零 → 恒 PASS；崩溃仅随 gollvm 出现/消失。

## 与 issue2 的对应关系（issue/issue2.md）

- §4.7 定位的修复缺口：cd24507a 只在 `!alloc->heap_` 分支清零——命名结果堆逃逸（本场景 `&res` 经接口被持有）时清零被跳过，正是本场景的形状；
- §5.1 CoreGo 对应路径：堆逃逸结果的 heapaddr 槽以 `Needzero` 注记入口清零（plive.go）——本场景是 CoreGo `zeroResults` + `Needzero` 两条路径中 `Needzero` 一条的行为级探针（g01/g03 打 zeroResults 路径）；
- §4 条件 3（晚写入拉长暴露窗口）：hold 建立逃逸 → GC → 写结果，窗口 = 函数入口 → `res = makeBigInfo(...)` 完成；
- §2.1 崩溃形态（unallocated span / marked free object）：脏堆预热制造同 size class 已释放 span 环境。

## 不变量

1. `victimEscape` 写后 GC 内全字段校验正确（合法值必须仍被正确扫描）；
2. 返回值 copy 全字段校验（12 指针 + 标量哨兵 + S + Pad）；
3. 跨 GC 后经 holder 读回的堆对象仍存活且全字段正确（`holder lost escaped result` 即 FAIL）；
4. weak 强引用断言：`res.A` 仍被强引用时 `weak.Value() != nil`；
5. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started == finished == workers`；
6. 心跳持续增长，watchdog 6 个周期无进展 → FAIL（附全栈）；
7. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
8. 最后一行 `STRESS PASS g04_escaped_result_heapaddr`——官方 Go 恒 PASS；**崩溃 = 编译器 bug**（heapaddr 槽未入口清零），PASS = 本轮未触发。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 确定性 ID/种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 100 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径（fatal error 现场） |

`.skip-envs` 含 `detector`：`efence=1` 破坏同 size class span 复用语义（脏堆预热的目标环境），本场景判据不依赖 efence。

## 失败判读

- 进程崩溃（`found bad pointer in Go heap` / `pointer ... to unallocated span` / `found pointer to free object` / `marked free object`）：**heapaddr 槽未入口清零**，返回前 GC 扫到陈旧堆地址（issue2 形态 A/B）——直接 FAIL；
- `holder lost escaped result`：逃逸结果被提前回收（holder 持有的堆对象漏标）；
- `victimEscape/post-gc` / `caller/post-return` / `caller/post-gc.held` canary mismatch：写入路径或跨 GC 存活损坏；
- `strong ref held but object collected`：weak 强引用断言失败——漏标判据；
- `no worker progress`：watchdog 超时（GC/预热卡死），附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- `BigInfo size=... want >=512`：场景自身结构退化（仅 preflight）；
- **PASS = 本轮未触发**：官方 Go 恒 PASS；gollvm 未修复时 window/soak 下高概率崩溃。

## 运行

```bash
SUITE=stress FILTER=g04 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=g04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=g04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 PROFILES=base ./run.sh
```

## 来源

- `issue/issue2.md` §4.7（cd24507a 的 `!heap_` 缺口）、§5.1（CoreGo Needzero 路径对应）、§2.1（崩溃形态）；
- `stress/g01_goret_dirty_slot`（同根机制，非逃逸 goret 槽载体；本场景为堆逃逸 heapaddr 载体变体）；
- `stress/x03_namedret_defer_rvo`（命名结果逃逸/改写形状）、`stress/x01_aggregate_statepoint`（harness 骨架）。
