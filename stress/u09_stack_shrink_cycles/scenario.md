---
id: u09_stack_shrink_cycles
priority: P0
covers: [L04, L17]
requires: []
default_seconds: 60
max_workers: 8
---

# 场景

栈**收缩**循环：与 s03（栈增长/展开）相反，专打 `shrinkstack → copystack` 的指针重定位路径。

每个 worker 每轮维护一组跨收缩存活的栈根：`[32]*node` 局部指针数组、`interface{}` 装箱指针、slice 头。随后按**锯齿梯子**反复进出深递归：

- 深轮：`depth → depth/2 → depth/4 → 32`（`STRESS_DEPTH` 默认 1024）逐级递归并返回；
- 浅轮：`depth/8 → 32`，与深轮交替，形成锯齿。

每级递归返回后，worker 在浅栈处阻塞于 channel，由专用 shrink driver 代为执行 `runtime.GC()`，最浅一级执行 `debug.FreeOSMemory()`。此时该 goroutine 栈使用远小于分配尺寸，GC 会将其栈 shrink（`used < 1/4 alloc` 时反复折半），通过 `copystack` 搬移栈并重定位跨阻塞点存活的指针；恢复后立即逐槽校验 canary，并校验阻塞点帧内另一组局部指针数组。最后在收缩后的栈区域新建一个 64 槽指针数组做残留区域扫描校验。

### 与 s03 的差异

- s03 主目标是增长/展开（morestack 栈复制、展开路径、大帧），`FreeOSMemory` 收缩只是顺带的收尾动作；
- u09 把**收缩触发条件**做成核心结构：goroutine 在浅栈上阻塞，由外部 driver 发起 GC，确保 shrinkstack 条件成立，反复制造"长高→收缩搬移→再长高"的循环；
- u09 在阻塞点帧与调用者帧同时放置跨收缩存活的指针数组/interface/slice 头，逐项校验重定位；s03 无跨收缩存活根的断言；
- u09 深/浅轮交替锯齿，并统计 `shrink_cycles`（收缩点次数）；这也是 `GODEBUG=gcshrinkstackoff` 对照的目标——关掉收缩后本场景应仍正确，但不再覆盖搬移路径。

## 不变量

1. 每级 `descend` 的 checksum 等于公式 `2×Σ magic(baseID+k)`（k=0..depth）；
2. 每帧返回路径上局部指针与 8 个指针槽 canary 正确；
3. 收缩前/后，调用者帧内 `hold[32]*node`、interface 槽、slice 头逐项 canary 正确——任何栈搬移漏重定位立即 FAIL；
4. 阻塞点帧内 `local[16]*node` 在 GC 恢复后逐槽正确；
5. 收缩后新建帧的 64 槽指针数组全部校验通过；
6. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started == finished == workers`；`shrink_cycles > 0`；
7. 收尾 quiesce 后连续 6 次采样 `NumGoroutine <= 基线+4`；
8. 最后一行 `STRESS PASS u09_stack_shrink_cycles`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤8） |
| `STRESS_DEPTH` | 1024 | 深轮递归深度（64..4096） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | canary 种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 12 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `phase=u09.descend ... checksum=... want=...`：递归返回路径指针内容损坏；
- `phase=u09.post-shrink ... canary 失配（栈搬移漏重定位）`：收缩/`copystack` 时局部指针数组、interface 或 slice 头未被正确重定位（本场景最敏感的 UAF 信号）；
- `phase=u09.shrinkpoint ... 收缩后 canary 失配`：浅栈阻塞点帧的指针槽未被重定位；
- `phase=u09.residual`：收缩后残留栈区域扫描失配；
- `phase=watchdog`：心跳停滞，附全栈（可能是 driver GC 卡死）；
- `没有发生收缩点` / `fixed rounds`：场景结构退化或被提前终止；
- `goroutine 泄漏`：收尾后未回基线；
- 只在 clobberfree/soak 下失败：典型的收缩搬移 UAF；base 也失败多为 checksum/控制流逻辑错误。

## 运行

```bash
SUITE=stress FILTER=u09 STRESS_SECONDS=60 PROFILES=soak,soak-single ./run.sh
SUITE=stress FILTER=u09 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=u09 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 PROFILES=base ./run.sh
```
