---
id: u02_preempt_scan_window
priority: P0
covers: [L03, L17, L18]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

抢占 × 栈扫描窗口风暴。三种持针形态的 worker 并行运行，都被刻意保持为“无调用/极少调用”的循环，使指针在异步抢占到来时活跃在**寄存器或尚未 spill 的栈槽**中（对齐调研报告 A13：安全点处指针只在寄存器中、stackmap 不含寄存器 → 漏标 → UAF）：

1. **interface 方法热切换**：interface 值在 3 个实现（`implA` 指针字段在前 / `implB` 指针字段居中 / `implC` 指针字段在后）间逐 chunk 轮转，每个 chunk 内先跑 4096 次无调用窗口（动态下标轮转读取登记节点），窗口结束立即调用当前 interface 的方法（receiver 指针在窗口内持续存活），校验返回的 canary magic 与 receiver 标签。
2. **method value + 闭包链**：绑定 receiver 的 method value（`fixture.bump`）循环调用；闭包 `readAll` 捕获 `[]*node` 指针数组并在内部无调用循环读取，`outer` 再包一层形成闭包链；每 chunk 校验 method value 返回值、闭包校验和与链上返回值。
3. **混合聚合**：局部 `struct{p *node; inner [4]*node; tag int64}` 在 chunk 的无调用循环里按 5 个字段动态轮转访问，窗口结束逐字段全量校验；聚合的 5 个指针位于栈槽，同时以运行时下标访问，迫使字段地址化。

**抢占窗口放大**：独立 `gcStorm` goroutine 无间隙分配垃圾并高频 `runtime.GC()`（GC 需要扫所有栈 → 对运行中 goroutine 发起抢占）。主 goroutine 只在阶段边界切换 `GOMAXPROCS`：显式 `STRESS_GMX`（如 `1,2,4`）优先；未显式设置时，若进程初始 GOMAXPROCS ≥ 2 则自动按 `1,2` 两阶段切换；初始为 1（`soak-single`）时不切，保持 profile 语义。GOMAXPROCS=1 时无多余 P 可调度，长循环只能靠 SIGURG 异步抢占停下，窗口最窄。计时模式按阶段均分时长，固定轮数模式按阶段均分轮数。

**挂起多样性**：每个 worker 每轮在 `runtime.Gosched()`、无缓冲 channel 与 relay goroutine 的两次 rendezvous 阻塞收发、微秒级 `time.Sleep`（50–300µs）三者间轮转；挂起前持有的接口值 / method value / 闭包数组 / 聚合指针在挂起点保持存活，恢复后立即做全量校验。

**canary**：节点 `node{magic,id,pad0,pad1}` 的 magic 由 `seed+id` 确定性哈希生成，pad 由 id 线性派生；任何窗口内读取不匹配立即 FAIL（clobberfree/efence 下 UAF 当场现形）。每个 worker 每轮在显式 GC 前后各做一轮全量校验；全局累计 canary 校验次数。

**finalizer 探针**：循环外仍被全局 anchorStore 持有的 4 个 anchor 注册 finalizer；运行前、阶段边界、收尾多轮 GC 后校验内容，且终结计数必须为 0——可达对象被终结即 FAIL。

统一 harness：`STRESS_SECONDS` / `STRESS_WORKERS` / `STRESS_HEARTBEAT` / `UAF_SEED` / `STRESS_FIXED_ROUNDS` / `STRESS_CRASH_FILE`、心跳 watchdog、`failf`、指标（`preempt_checks`、`chunk_checks`、`gc_storm`、`worker_gc` 等）、收尾 quiesce 与 goroutine 基线；末行 `STRESS PASS u02_preempt_scan_window`。仅标准库，无墙钟断言。

## 与 s04 的差异

`s04_preempt_storm_soak` 打的是“无调用长循环**能否**被异步抢占、被抢占/GC 后闭式校验和是否仍成立”——核心是抢占可达性与循环结果。`u02_preempt_scan_window` 打的是“**被抢占那一瞬间**指针只在寄存器 / 未 spill 栈槽时的扫描正确性”——核心是持针形态（interface receiver、method value 的 receiver、闭包捕获数组、聚合栈槽）在安全点栈图上的完整性，并用确定性 canary、receiver 恒等性、finalizer 锚点把漏标导致的 UAF 变成当场可判定的 FAIL；同时增加三种主动挂起点与 1↔2 阶段切换，覆盖“抢占点/挂起点/扫描点”三类窗口。

## 不变量

1. 无调用窗口内每次读取的节点指针非空，`magic/id` 与登记 canary 一致；interface 方法返回的 magic 等于登记值，receiver 身份（`raw() == entry.n`）不变。
2. method value 调用返回 `fixtureMagic+r`；闭包 `readAll` 返回全部指针 magic 之和 `ptrSum`；闭包链 `outer` 返回 `ptrSum+r`；聚合 5 字段轮转读取与逐字段全量校验均一致。
3. 三种挂起点（Gosched / 无缓冲 channel 往返 / 微秒 Sleep）恢复后，挂起期间存活的指针全部仍指向原对象且内容不变；channel 往返指针恒等。
4. 每个 chunk 窗口后、每次挂起后、每次显式 GC 前/后，全量 canary 校验通过；clobberfree/efence 下不出现任何 id/magic/pad 漂移（UAF 当场 FAIL）。
5. 全局 anchor 在整个运行期可达，finalizer 终结计数恒为 0，anchor 内容不变。
6. 每 worker 的原子累加值等于本地累加值，全局原子和等于各 worker 之和（int64 回绕）；固定轮数模式下每 worker 恰好完成 `STRESS_FIXED_ROUNDS` 轮。
7. 心跳持续增长、watchdog 无停滞；收尾后 helper/relay goroutine 全部退出，goroutine 数回到基线容差内。
8. 末行打印 `STRESS PASS u02_preempt_scan_window`。

## 参数

- `STRESS_SECONDS`（默认 60；0=固定轮数）
- `STRESS_WORKERS`（默认 6，最大 32；按 `i%3` 轮流分配三种持针形态）
- `STRESS_HEARTBEAT`（默认 5s，停滞 6 个周期判 FAIL）
- `UAF_SEED`（默认 1，决定全部 canary magic）
- `STRESS_FIXED_ROUNDS`（默认 32，仅 `STRESS_SECONDS=0`；按阶段均分）
- `STRESS_GMX`（可选，如 `1,2,4`）：显式指定时按列表在阶段边界切换 GOMAXPROCS；未指定且初始 GOMAXPROCS≥2 时自动 `1,2`；未指定且初始为 1 时不切（`soak-single`）

## 失败判读

- `phase=iface-A/B/C ... receiver tag corrupted`：接口 receiver 指针或其标签被破坏（实现间热切换时 receiver 丢失/串型）；
- `phase=iface-burst|closure-burst|mix-burst ... corrupted`：无调用窗口内寄存器/未 spill 栈槽中的指针指向错误对象或对象已被回收（A13 漏标 UAF）；
- `phase=closure-capture|closure-chain ... sum/want`：闭包捕获的 `[]*node` 内容被破坏或闭包链返回错误；
- `phase=methodvalue ... bump/want`：绑定 receiver 的 method value 丢失/串了 receiver；
- `phase=chan ... pointer identity lost`：无缓冲 channel 往返后指针不恒等；
- `phase=*-post-suspend ... corrupted`：Gosched/Sleep/channel 挂起点恢复后，挂起期间持有的指针被错误扫描/回收；
- `phase=verify-pre-gc|verify-post-gc ... corrupted`：显式 GC 前后全量校验发现可达对象被破坏；
- `phase=anchor ...`：循环外可达 anchor 被 finalizer 终结或内容损坏（可达性/扫描错误）；
- `phase=final ... rounds/atomic sum/started`：轮次丢失、原子累加丢失更新或 worker 未正常收尾；
- `watchdog stalled`：长时间等不到安全点/STW 卡死（内层）；外层由 runner `timeout -s QUIT` 兜底；
- 仅在 `clobberfree`/`efence` 下失败：UAF；`soak-noasync` 为 `asyncpreemptoff=1` 控制组（此时依赖循环返回/挂起点等协作式安全点）。

## 运行

```bash
SUITE=stress FILTER=u02 STRESS_SECONDS=3 PROFILES=soak,soak-single,soak-noasync REPEAT=1 TIMEOUT=90 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=u02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree REPEAT=1 TIMEOUT=90 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=u02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 BUILD_FLAGS=-race PROFILES=base REPEAT=1 TIMEOUT=300 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
STRESS_GMX=1,2,4 SUITE=stress FILTER=u02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=12 PROFILES=base ./run.sh
```
