---
id: y02_multiret_slots
priority: P0
covers: [L17]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

多返回值 × 大匿名结果并置 × 错误路径提前 return。x05/g01/g03 都打单结果槽；本场景打多结果并置的槽布局（相邻结果槽、不同位图形状、标量/指针/接口混合）：

- **四个组合**：`(Big, error)`、`(int, Big, *node)`、`(Big, Big2)`、`(Big, Big2, error)`——Big 528B（17 指针跨两个位图 word）、Big2 224B（8 前置指针 + 尾指针），全部匿名结果；
- **成功路径分步写**：r1 写 → `gcGate()` → r2 写 → `gcGate()`，穷举“部分结果已写/未写”扫描窗口；
- **错误路径**：`trig` 时提前 `return` 前先 `gcGate()`（其余结果槽未初始化被扫描——**潜在 UAF 窗口**），return 后校验各槽**零值**——槽未清零（脏值泄漏）即确定性现形；
- **早/晚消费交错**：第 1 个结果立刻校验、第 2 个结果先 GC 再校验、第 3 个（error/*node）经 chan 送走后 GC 再收回校验；
- **脏栈铺垫**：dirtyRec（深度 `d = 3 + i%96` 各层帧刷满指向短命堆对象的指针，depth 0 `gcGate()` 清扫成悬挂）+ victimRec 同深度下降（同签名同 carrier → 帧尺寸一致，多结果槽落在旧 dirtyRec 帧脏字上）；
- 每轮 8 个下降（4 组合 × 成功/错误路径）+ weak 强引用探针。

## 与 x05 命中形状的关系与差异

- x05 是单结果匿名槽 + panic/Goexit 载体；本场景是**多结果并置**——编译器对多返回的槽分配/零化/栈图通常按“结果块”整体处理，并置大槽 + 相邻窗口的组合是 x05 未覆盖的布局族；
- 与 g05（命名结果 + 19 边界 GC 穷举）的差异：g05 用命名结果逐个字段写；本场景是**匿名**多返回，窗口由“结果间/返回前”的 gate 与**错误路径提前 return**制造（g05 无错误路径未初始化槽形态）；
- 判据相同：官方 Go 恒 PASS；崩溃 = 编译器 bug（结果槽未入口清零/栈图漏标）；`FAIL canary/零值` = 确定性 UAF。

## 不变量

1. 成功路径各结果 canary（Big/Big2 全字段 + n 标量 + *node + error code/seed）与 `(seed,base)` 公式一致；
2. 错误路径返回后 `Big`/`Big2`/标量/指针槽**全零**（提前 return 的其余结果槽未清零即现形）；
3. 消费交错：早消费结果、GC 后校验结果、chan 往返结果全部完整；
4. weak 强引用断言：结果仍被强引用时 `weak.Value() != nil`；
5. 每轮 trig 路径数 == 4 × rounds（会计校验）；深度抖动覆盖 `[3,98]`；
6. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；che/chn 收尾为空；`started == finished == workers`；
7. 心跳持续增长，watchdog 6 个周期无进展 → FAIL（附全栈）；
8. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
9. 最后一行 `STRESS PASS y02_multiret_slots`——官方 Go 恒 PASS；**崩溃 = 编译器 bug**，PASS = 本轮未触发。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 确定性 ID/种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 40 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径（fatal error 现场） |

`.skip-envs` 含 `detector`：`efence=1` 破坏同 size class span 复用语义且大量小对象必然内存爆炸，本场景判据不依赖 efence。

## 失败判读

- 进程崩溃（`found bad pointer in Go heap` / `pointer ... to unallocated span` / `found pointer to free object` / `marked free object`）：**多结果槽未入口清零/栈图漏标**，某 gate GC 扫到脏槽——直接 FAIL；
- `canary mismatch` / `nil *node`：对象被提前回收或复用——**确定性 UAF**；
- `错误路径结果槽未清零`（`Ptrs[i]=... want=nil`）：提前 return 后槽未零化、脏值泄漏——x05 根因的直接形态；
- `n=... want=...` / `err canary ...`：多返回值写入路径损坏；
- `strong ref held but collected`（weak）：结果槽漏标——最直接判据；
- `trig 路径=... want=...`：叶子会计损坏；`no worker progress`：watchdog 超时，附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- 只在 `window` 失败：漏标/并发窗口；`detector` 崩溃：释放对象被再次访问（UAF）。

## 运行

```bash
SUITE=stress FILTER=y02 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=y02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=200 PROFILES=window ./run.sh
SUITE=stress FILTER=y02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=y02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 PROFILES=base ./run.sh
```

## 来源

- `stress/x05_goexit_panicframe`（匿名按值返回槽命中点）、`stress/g05_multiret_deep_frames`（多返回值边界 GC 穷举方法论）、`stress/g01_goret_dirty_slot`（dirtyRec/victimRec 手法）；
- `issue/issue2.md` §4（缺陷闭环：槽未初始化 × 栈图描述 × 窗口扫描 × 脏字内容）；
- `docs/research/stress_batch4_design.md` y02 条目；DIMENSIONS.md L17（栈映射）。
