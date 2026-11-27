---
id: y03_generic_ret_slot
priority: P0
covers: [L01, L17]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

泛型返回侧大值返回：`Box[T].Get() T` × 8 个实参类型（不同尺寸/指针位图），字典（interface itab 派发）与单态化（直接调用）双路径，泛型函数返回局部聚合（RVO/拷贝交互）：

- **8 个实参类型**：T1 稀疏 3 指针 136B、T2 纯指针 14×8=112B、T3 16 指针+words 200B、T4 两端指针+31×u64 块 272B、T5 8 指针+4 string 136B、T6 前后指针组+28×u64 288B、T7 24 指针 240B、T8 纯指针 32×8=256B（同时作为下降 carrier）——覆盖不同位图密度与跨 word 布局；
- **每类型三路调用**（同一叶子帧内三槽并置）：
  1. `Box[T]{...}.Get()` 直接调用（单态化路径）；
  2. `var g getterIface[T] = Box[T]{...}; g.Get()` 经实例化泛型接口 itab 派发（字典路径）；
  3. `buildLocal[T]`：F-bounded 约束（`T fillable[T]`）的泛型函数返回局部聚合——`var v T; v = v.fill(...); return v`；
- **返回前 gcGate（槽仍脏）+ 返回后 gcGate 再分字段消费**：三路结果都在 GC 后校验 canary；
- **脏栈铺垫**：dirtyRec（`d = 3 + i%96` 各层帧刷满指向短命堆对象的指针，depth 0 `gcGate()` 清扫成悬挂）+ victimRec 同深度下降（同签名同 carrier T8 → 帧尺寸一致，泛型返回槽落在旧 dirtyRec 帧脏字上）；
- weak 强引用探针每轮一次。

## 与 x05 命中形状的关系与差异

- x05 是非泛型匿名返回槽 + panic 载体；本场景打**泛型实例化**的返回侧——实例化函数有独立的栈图/结果槽生成路径（字典参数、shape stenciling、接口 wrapper），是 x05/g01 未覆盖的编译器 IR 形状族；
- 与 x01（泛型只打传参侧）互补：本场景是返回侧 + 接口派发 + 泛型局部聚合返回；
- 判据相同：官方 Go 恒 PASS；崩溃 = 编译器 bug（实例化返回槽未入口清零/字典路径栈图漏标）；`FAIL canary` = 确定性 UAF。

## 不变量

1. 每类型三路返回值（direct/iface/buildLocal）分字段 canary 与 `(seed,base)` 公式一致（T5 含 string 内容哨兵）；
2. 每轮 leaf 调用数 == 24（8 类型 × 3 路，会计校验）；
3. weak 强引用断言：结果仍被强引用时 `weak.Value() != nil`；
4. 深度抖动覆盖 `[3,98]`，metrics 报 `depth_min/depth_max/depth_avg`；
5. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started == finished == workers`；
6. 心跳持续增长，watchdog 6 个周期无进展 → FAIL（附全栈）；
7. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
8. 最后一行 `STRESS PASS y03_generic_ret_slot`——官方 Go 恒 PASS；**崩溃 = 编译器 bug**，PASS = 本轮未触发。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 确定性 ID/种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 30 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径（fatal error 现场） |

`.skip-envs` 含 `detector`：`efence=1` 破坏同 size class span 复用语义且大量小对象必然内存爆炸，本场景判据不依赖 efence。

## 失败判读

- 进程崩溃（`found bad pointer in Go heap` / `pointer ... to unallocated span` / `found pointer to free object` / `marked free object`）：**实例化返回槽未入口清零/字典路径栈图漏标**，某 gate GC 扫到脏槽——直接 FAIL；
- `canary mismatch` / `nil *node` / `S[..]=... want=...`：对象被提前回收或复用，或泛型值拷贝损坏——**确定性 UAF**；
- `strong ref held but collected`（weak）：返回槽漏标——最直接判据；
- `leaf 调用=... want=...`：叶子会计损坏；
- `no worker progress`：watchdog 超时（GC 卡死），附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- 只在 `window` 失败：漏标/并发窗口；`detector` 崩溃：释放对象被再次访问（UAF）。

## 运行

```bash
SUITE=stress FILTER=y03 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=y03 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=200 PROFILES=window ./run.sh
SUITE=stress FILTER=y03 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=y03 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 PROFILES=base ./run.sh
```

## 来源

- `stress/x05_goexit_panicframe`（匿名按值返回槽命中点）、`stress/x01_aggregate_statepoint`（泛型传参侧）、`stress/g01_goret_dirty_slot`（dirtyRec/victimRec 手法）；
- `issue/issue2.md` §4（缺陷闭环四条件）；
- `docs/research/stress_batch4_design.md` y03 条目；DIMENSIONS.md L01（泛型）与 L17（栈映射）。
