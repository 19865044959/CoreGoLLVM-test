---
id: y01_anon_ret_dirty_slot
priority: P0
covers: [L17, L18]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

x05 命中形状（大对象 + 匿名按值返回槽未清零，GC 扫到旧已释放数据）的**最小纯化 + 压力拉满**，去掉 panic/Goexit 载体，专打槽本身：

- `makeBig(...) Big`：Big 528B（17 个指针字段跨两个位图 word + words/pad 非指针混合），**无命名结果**——匿名返回槽超寄存器返回阈值，槽在调用方帧；
- **脏栈 maker**：`dirtyRec` 深度 `d = 3 + i%96` 递归，各层帧 carrier + 返回槽全部刷满指向短命堆对象的指针，depth 0 处 `gcGate()` 清扫成悬挂（issue2 dirtyRec 手法）；`victimRec` 同签名同 carrier 同深度下降——帧尺寸一致，`makeBig` 的返回槽落在旧 dirtyRec 帧的脏字上；
- **双 GC 窗口**：调用方拿到返回值**前**先 `gcGate()`（noinline helper 承载 GC——槽仍脏，栈图不得把脏字当活指针）；`makeBig` 返回与首次读**之间**再 `gcGate()`（槽已写入，栈图必须标活全部指针）；之后分字段消费校验 canary；
- **变体**：`makeBigStepwise` 分字段部分写入（每写一个指针字段后 GC——穷举 18 个写入窗口）；同一调用点 4 轮复用（同栈位反复，每轮调用前 GC 扫上一轮已死的旧值）；返回值直接送 chan / 直接装箱 interface；weak 强引用探针。

## 与 x05 命中形状的关系与差异

- x05 是 panic/Goexit 载体混合形态（panic 记录保活死栈对象 + 展开路径 GC）；本场景剥离全部载体，只留“匿名按值返回大对象 × 脏槽 × GC 扫描窗口”，压力拉满：深度抖动 + 每轮 ~40 次显式 GC + 分字段部分写入穷举；
- 与 g01（命名结果 + 接口晚写闭包）的差异：本场景是**匿名**返回槽（无命名结果、无 `*BigInfo` 地址取用），写入方就是返回语句本身，不依赖闭包晚写——窗口由 caller 侧前/后 gate 与 callee 侧分步写入制造；
- 判据与 g01 相同：官方 Go 恒 PASS；崩溃 = 编译器 bug（返回槽未入口清零）；`FAIL canary` = 确定性 UAF（对象被提前回收/复用）。

## 不变量

1. 每个变体返回的 `Big` 全部 17 个指针字段 canary + Words/Pad/Seq 哨兵与 `(seed,base)` 确定性公式一致（后 gate 校验 + caller 校验两段）；
2. `leafIface` 装箱值跨 GC 反装箱后完整；chan 值跨期间全部 GC 后 drain 校验完整；
3. weak 强引用断言：结果仍由本地变量强引用时 `weak.Value() != nil`；
4. 深度抖动覆盖 `[3,98]`，metrics 报 `depth_min/depth_max/depth_avg`；
5. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮，chan 残留数 == 轮数；`started == finished == workers`；
6. 心跳持续增长，watchdog 6 个周期无进展 → FAIL（附全栈）；
7. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
8. 最后一行 `STRESS PASS y01_anon_ret_dirty_slot`——官方 Go 恒 PASS；**崩溃 = 编译器 bug**（匿名返回槽未入口清零/栈图漏标），PASS = 本轮未触发。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 确定性 ID/种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 60 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径（fatal error 现场） |

`.skip-envs` 含 `detector`：`efence=1` 逐对象 page 化，破坏同 size class span 复用语义且大量小对象必然内存爆炸，本场景判据不依赖 efence。

## 失败判读

- 进程崩溃（`found bad pointer in Go heap` / `pointer ... to unallocated span` / `found pointer to free object` / `marked free object`）：**匿名返回槽未入口清零/栈图漏标**，某 gate GC 扫到脏槽——直接 FAIL（run.sh 按非零退出 + crash 文件判定）；
- `canary mismatch` / `nil *node`：对象被提前回收或复用（返回后 gate 未标活/调用方槽漏标）——**确定性 UAF**；
- `strong ref held but collected`（weak）：结果仍强引用时对象被回收——最直接漏标判据；
- `chan 残留 != 轮数`：叶子 chan 变体计数损坏；
- `no worker progress`：watchdog 超时（GC 卡死/窗口饿死），附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- `Big size=... want >=512`：场景自身结构退化（仅 preflight）；
- 只在 `window` 失败：漏标/并发窗口；`detector` 崩溃：释放对象被再次访问（UAF）。

## 运行

```bash
SUITE=stress FILTER=y01 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=y01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=300 PROFILES=window ./run.sh
SUITE=stress FILTER=y01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=y01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 PROFILES=base ./run.sh
```

## 来源

- `stress/x05_goexit_panicframe`（命中形状：大对象 + 匿名按值返回槽未清零）、`stress/g01_goret_dirty_slot`（dirtyRec/victimRec 同深度下降手法、zeroResults 对照结论）；
- `issue/issue2.md` §2.1（脏栈制造 + 深度抖动 d=3+i%96）、§4（缺陷闭环四条件：槽未初始化 × 栈图描述 × 晚写窗口 × 脏字内容）；
- `docs/research/stress_batch4_design.md` y01 条目；DIMENSIONS.md L17（栈映射）与 L18（GC 生命周期/写屏障）。
