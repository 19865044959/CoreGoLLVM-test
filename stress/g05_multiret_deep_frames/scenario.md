---
id: g05_multiret_deep_frames
priority: P1
covers: [L17, L04]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

多返回值 × 深帧复用 × 逐边界 GC：issue2（goret 结果槽未入口清零，§4 四条件）与 issue1（深调用 → 浅返回栈形，§1）的**组合**。把「结果声明 → 写入完成」窗口的每个边界都变成一次显式 GC 注入：

- **多返回值（指针 + 聚合混合）**：`multiVictim() (p *payload, info BigInfo, n int64)`——`n` 标量先写、`info` 分字段逐步写、`p` 指针最后写（`p` 的结果槽在全部前置边界 GC 窗口内都未初始化）；
- **穷举边界注入**：`info` 的每个指针字段、每个标量组、`Pad`、`S` 写入间各插一次 `boundaryGC`（共 **19 个边界**，每步后 GC + 同 size class poison 分配）——GC 精确落在每个「部分写入完成」状态上，任何漏清零的槽位被按位图当活指针扫描即崩；
- **深帧复用**：`dirtyRec`（深度抖动 `d = 3 + i%24` 递归刷满指向短命堆对象的指针 + depth 0 GC 清扫成悬挂）与 `victimMultiRec` 同构下降（d 派生偏移、同 byval `BigInfo` 帧尺寸一致），使 `multiVictim` 的三个结果槽全部落在旧 dirtyRec 帧的脏字上；
- 每层下降帧携带三个结果（p/info/n 复制上抛），深帧 + 多返回值槽叠加。

官方 Go `zeroResults` 入口清零 → 恒 PASS；崩溃（`found bad pointer in Go heap` / `pointer to free object` / `marked free object`）仅随 gollvm 出现/消失。

## 与 issue 的对应关系（issue/issue2.md、issue/issue1.md）

- issue2 §2.1 的 dirtyRec/victimRec 同深度下降 + byval 帧尺寸一致 + 深度抖动机制：原样复刻（抖动模取 24，因每轮 19 个 GC 边界）；
- issue2 §4 缺陷闭环：结果槽在调用方帧未初始化（条件 1）+ 栈图无条件描述（条件 2）+ 窗口被 19 个边界 GC 穷举扫描（条件 3 的强化版：不再依赖晚写闭包，任何边界都是注入点）+ 槽内容为陈旧栈垃圾（条件 4）；
- issue2 §6.1 验证方法（"并行必中轰炸"：多 worker × 放大器 × 多轮）——本场景把「偶现 GC 落点」变成「每个边界必测一次」（x13_safepoint_matrix 方法论在多返回值形状上的应用）；
- issue1 §1 深调用 → 浅返回栈形：dirtyRec/victimMultiRec 的深下降在 GC 窗口期制造深栈扫描环境。

## 不变量

1. 每个边界 GC 后的部分写入状态在官方 Go 下均为合法状态（入口零化的语义结果）；写完后 `n == base+7`、`p` canary、`info` 全字段校验；
2. caller 返回后 `p`/`info`/`n` 三路全量校验；
3. weak 强引用断言：`p` 仍由本地强引用时 `weak.Value() != nil`；
4. 深度抖动覆盖 `[3,26]`；边界数恒为 19（`boundary_gc == rounds*19`）；固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started == finished == workers`；
5. 心跳持续增长，watchdog 6 个周期无进展 → FAIL（附全栈）；
6. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
7. 最后一行 `STRESS PASS g05_multiret_deep_frames`——官方 Go 恒 PASS；**崩溃 = 编译器 bug**（结果槽未入口清零），PASS = 本轮未触发。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 确定性 ID/种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 60 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径（fatal error 现场） |

`.skip-envs` 含 `detector`：`efence=1` 破坏同 size class span 复用语义，本场景判据不依赖 efence。

## 失败判读

- 进程崩溃（`found bad pointer in Go heap` / `pointer ... to unallocated span` / `found pointer to free object` / `marked free object`）：**结果槽未入口清零**，某边界 GC 扫到脏槽（issue2 形态 A/B）——直接 FAIL，结合心跳/metrics 定位最后完成的轮次与深度；
- `n=... want=...` / `caller/post-return.* canary mismatch`：多返回值写入路径或上抛拷贝损坏；
- `strong ref held but object collected`：weak 强引用断言失败——漏标判据；
- `no worker progress`：watchdog 超时（边界 GC 卡死），附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- `BigInfo size=... want >=512`：场景自身结构退化（仅 preflight）；
- **PASS = 本轮未触发**：官方 Go 恒 PASS；gollvm 未修复时 window/soak 下高概率崩溃。

## 运行

```bash
SUITE=stress FILTER=g05 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=g05 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=g05 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 PROFILES=base ./run.sh
```

## 来源

- `issue/issue2.md` §2.1（脏栈/同构下降/深度抖动）、§4（缺陷闭环四条件）、§6.1（轰炸验证方法）；
- `issue/issue1.md` §1（深调用 → 浅返回栈形）；
- `stress/g01_goret_dirty_slot`（同根机制）、`stress/x13_safepoint_matrix`（逐边界穷举注入方法论）、`stress/x01_aggregate_statepoint`（harness 骨架）。
