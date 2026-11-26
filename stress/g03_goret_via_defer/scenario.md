---
id: g03_goret_via_defer
priority: P1
covers: [L17]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

goret 槽 + defer 写入：issue2 的**变体**。与 g01 同根（goret 结果槽未入口清零，未初始化窗口被 GC 按位图扫描），但把「经接口调用的堆逃逸闭包晚写入」换成 **defer 写入**：

- 命名结果 `BigInfo`（552B，多指针位图）由 **defer 函数写入**——defer 到期前（函数体执行期）是未初始化窗口；
- **多 defer 链**：先执行的 defer 只 `runtime.GC()`（窗口被扫描 = 缺陷触发点），后执行的 defer 才写结果；写后再次 GC + 写后读校验；
- **defer 前制造脏栈 + GC**：函数体内先跑 `dirtyRec`（深度抖动 `d = 3 + i%96` 递归刷满指向短命堆对象的指针，depth 0 处 GC 清扫成悬挂），再 `victimRecDefer` 同深度下降（同 byval `BigInfo` 帧尺寸一致），底部 `victimDefer` 的 goret alloca 落在旧 dirtyRec 帧的脏字上；
- 结果校验**写后读**：defer 写入后、caller 返回后、weak 强引用断言三处。

官方 Go `zeroResults` 入口清零 → 恒 PASS；崩溃（`found bad pointer in Go heap` / `pointer to free object` / `marked free object`）仅随 gollvm 出现/消失。

## 与 issue2 的对应关系（issue/issue2.md）

- §2.1 的 victim/dirtyRec/victimRec 同深度下降 + byval 帧尺寸一致 + 深度抖动机制：本场景原样复刻，仅把「经接口调用、堆逃逸的闭包晚写入」（g01 复刻）替换为 defer 写入——覆盖 §4 条件 3（晚写入拉长暴露窗口）的另一载体形状（deferreturn 路径）；
- §4 条件 1/2/4：槽在调用方帧未初始化、栈图无条件描述该槽、窗口期槽内容为陈旧栈垃圾——与 g01 相同；
- §5.1 CoreGo 对应：`zeroResults` 入口清零——官方 Go 下 defer 窗口也是零值，恒 PASS；
- 与 g01 的分工：g01 = 闭包经接口晚写（containerd 原形状）；g03 = defer 晚写（同缺陷的第二载体），二者互补。

## 不变量

1. defer 写后读：`victimDefer` 内写 defer 后 `checkBigInfo` 全字段正确；写后 GC 再校验（合法值必须仍被正确扫描）；
2. caller 返回后全字段校验（12 指针 + 标量哨兵 + S + Pad）；
3. weak 强引用断言：结果仍由本地强引用时 `weak.Value() != nil`（对象被提前回收 = 漏标/UAF）；
4. 深度抖动覆盖 `[3,98]`；固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started == finished == workers`；
5. 心跳持续增长，watchdog 6 个周期无进展 → FAIL（附全栈）；
6. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
7. 最后一行 `STRESS PASS g03_goret_via_defer`——官方 Go 恒 PASS；**崩溃 = 编译器 bug**（goret 槽未入口清零），PASS = 本轮未触发。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 确定性 ID/种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 100 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径（fatal error 现场） |

`.skip-envs` 含 `detector`：`efence=1` 破坏同 size class span 复用语义，本场景判据不依赖 efence。

## 失败判读

- 进程崩溃（`found bad pointer in Go heap` / `pointer ... to unallocated span` / `found pointer to free object` / `marked free object`）：**goret 结果槽未入口清零**，GC 扫到脏槽（issue2 形态 A/B）——直接 FAIL；
- `defer-write` / `defer-post-gc` / `caller/post-return` canary mismatch：defer 写入路径或扫描后结果损坏；
- `strong ref held but object collected`：weak 强引用断言失败——漏标判据；
- `no worker progress`：watchdog 超时（defer/GC 卡死），附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- `BigInfo size=... want >=512`：场景自身结构退化（仅 preflight）；
- **PASS = 本轮未触发**：官方 Go 恒 PASS；gollvm 未修复时 window/soak 下高概率崩溃。

## 运行

```bash
SUITE=stress FILTER=g03 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=g03 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=g03 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 PROFILES=base ./run.sh
```

## 来源

- `issue/issue2.md` §2.1（victim/dirtyRec/victimRec/深度抖动）、§4（缺陷闭环四条件）、§5.1（zeroResults 对应）；
- `stress/g01_goret_dirty_slot`（同根机制，闭包载体；本场景为 defer 载体变体）；
- `stress/x03_namedret_defer_rvo`（defer 改写命名返回 + deferreturn 处 GC 形状）、`stress/x01_aggregate_statepoint`（harness 骨架）。
