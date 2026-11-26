---
id: g01_goret_dirty_slot
priority: P0
covers: [L17, L18]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

goret 脏槽：issue2（BishengGo containerd `fatal error: found bad pointer in Go heap` / `pointer to free object`）的**核心复刻**。把 LLT 回归用例 `issue_goret_slot_dangling.go` 的触发条件做成可长跑、可穷举注入（深度抖动）、失败即现形（进程被 runtime 崩掉）的形态：

- **victim 同构**：`victim() (info BigInfo)`——`BigInfo` 552B（12 个指针字段 + 标量/`[]*payload`/padding 混合，指针位跨两个位图 word），超寄存器返回阈值（>64B 且 >9 指针寄存器预算），命名结果经 goret 内存槽返回；
- **晚写入闭包**：结果由经 `infoWriter` 接口调用、**堆逃逸**的闭包写入（与 containerd 经 `storage.MetaStore` 接口回调写 Stat/Update 结果同构），闭包签名带 `*BigInfo`，**写前闭包内 `runtime.GC()`** 扫栈；
- **脏栈制造**：`dirtyRec` 以深度 `d = 3 + i%96` 递归，各层帧的 `BigInfo` 副本 + 传入 carrier 全部刷成指向短命堆对象的指针，depth 0 处 `runtime.GC()` 把它们清扫成悬挂（槽在递归 safepoint 处已死，精确 liveness 下必被回收）；
- **同构下降**：`victimRec` 同深度下降（相同 byval `BigInfo` 保证帧尺寸一致），depth 0 处调用 `victim`——未初始化的 goret alloca 落在旧 `dirtyRec` 帧的脏字上，闭包写前 GC 按位图把这些脏字解释为活指针。

失败判定以**进程崩溃本身**为准（fatal error 时 `failf` 到不了，`STRESS_CRASH_FILE` 落盘留现场）：`found bad pointer in Go heap`（issue2 形态 A）、`pointer to free object` / `marked free object`（形态 B）即 FAIL。`window` profile（`gcstoptheworld=2,gccheckmark=1,clobberfree=1`）为放大器：每次 GC 全 STW，扫栈频率最高。

## 与 issue2 的对应关系（issue/issue2.md）

- §2.1 独立复现的全部机制：victim 同构（命名结果超阈值经 goret 内存槽返回）、接口调用 + 堆逃逸闭包晚写入（写前 GC）、`dirtyRec`/`victimRec` 同深度下降 + byval 帧尺寸一致、深度抖动 `d = 3 + i%96`、30000 轮长跑——本场景逐一复刻，默认固定轮数 100（自检），远端以 `STRESS_FIXED_ROUNDS=30000` 复刻 issue 强度；
- §4 缺陷闭环四条件：槽在调用方帧且未初始化（条件 1）、栈图无条件描述该槽（条件 2，无法从 Go 源码直接断言，靠形状复刻）、晚写入拉长暴露窗口（条件 3，闭包写前 GC）、窗口期槽内容为陈旧栈垃圾（条件 4，脏栈制造）；
- §5.1 CoreGo 对应：`zeroResults` 入口清零结果槽——官方 Go 下本程序恒 PASS；崩溃仅随 gollvm 出现/消失（§6.3 对照结论）。

## 不变量

1. 每轮 `victim` 返回的 `BigInfo` 全部 12 个指针字段 + 标量哨兵 + `S` 长度 + `Pad` 内容与 `(seed,base)` 确定性公式一致（闭包内写后 GC 校验 + caller 返回后校验两段）；
2. weak 强引用断言：结果仍由本地变量强引用时 `weak.Value() != nil`（对象被提前回收 = 漏标/UAF）；
3. 深度抖动覆盖 `[3,98]`，metrics 报 `depth_min/depth_max/depth_avg`；
4. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started == finished == workers`；
5. 心跳持续增长，watchdog 6 个周期无进展 → FAIL（附全栈）；
6. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
7. 最后一行 `STRESS PASS g01_goret_dirty_slot`——官方 Go 恒 PASS；**崩溃 = 编译器 bug**（goret 槽未入口清零），PASS = 本轮未触发。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 确定性 ID/种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 100 | `seconds=0` 时每 worker 轮数；**issue 强度 = 30000**（官方 Go 下约几十秒） |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径（fatal error 现场） |

`.skip-envs` 含 `detector`：`efence=1` 逐对象 page 化，破坏同 size class span 复用语义且 30000 轮必然内存爆炸，本场景判据不依赖 efence。

## 失败判读

- 进程崩溃（`fatal error: found bad pointer in Go heap` / `runtime: pointer ... to unallocated span` / `found pointer to free object` / `marked free object`）：**goret 结果槽未入口清零**，GC 扫到脏槽（issue2 形态 A/B）——直接 FAIL（run.sh 按非零退出 + crash 文件判定）；
- `canary mismatch` / `nil payload`：结果内容损坏或对象被回收/复用（闭包写入路径或漏标）；
- `strong ref held but object collected`：weak 强引用断言失败——最直接的漏标判据；
- `no worker progress`：watchdog 超时（GC 或递归卡死），附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- `BigInfo size=... want >=512`：场景自身结构退化（仅 preflight）；
- **PASS = 本轮未触发**：官方 Go 恒 PASS；gollvm 未修复时窗口/soak 下高概率崩溃。

## 运行

```bash
SUITE=stress FILTER=g01 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=g01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=30000 PROFILES=window ./run.sh   # issue 强度
SUITE=stress FILTER=g01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=g01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 PROFILES=base ./run.sh
```

## 来源

- `issue/issue2.md` §2.1（独立复现：victim/dirtyRec/victimRec/深度抖动/30000 轮）、§4（缺陷闭环四条件）、§5.1（zeroResults 对应）、§6.3（CoreGo 零复现对照）；
- LLT 回归用例 `test/tests/LLT/fixedbugs/issue_goret_slot_dangling.go`（20/20 崩 ≤7.3s，修复后 0 崩）；
- `stress/x01_aggregate_statepoint`（harness 骨架：canary/weak/poison/心跳/watchdog）。
