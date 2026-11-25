---
id: u11_timer_heap_churn
priority: P0
covers: [L19, L04]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

runtime timer 堆增删/sift 风暴：每 worker 每轮创建 `timersPerRound=4096` 个一次性 `time.AfterFunc`：

- 短周期（1µs..2ms）为主，其中每 32 个 short 立即 `Stop` 一个，制造 Stop 与到期竞态；
- 中周期（500ms）在“未到期快照”之后 Stop（若 Stop 返回 false，则由回调补记到期）；
- 长周期（1h）在快照之后 Stop，绝不允许到期。

每个 timer 的闭包只捕获自己的 `timerRec`（含确定性 canary 和 aux canary）。创建时先把 rec 放入 per-worker registry（map）再 arm timer，保证极短 timer 的回调自删除时条目已存在；回调先校验 canary magic，再 CAS `pending→fired`、从 registry 自删除。每轮结束等到 `pending==0`、registry 清空，再 `runtime.GC()`。

并发创建/删除让 runtime timer heap 持续大量 sift；回调在独立 goroutine 中读取捕获指针（L04 的 goroutine 回调路径），提前回收/漏标/回调丢失都会让 magic 失配或计数不一致。

与 n01 的差异：n01 打 Timer Reset/Stop 风暴、AfterFunc 自重挂、ticker/context/TCP；u11 打**一次性 AfterFunc 批量 arm + 大量到期/停止 sift + 回调指针与 registry 自删除**，不做重挂。

# 不变量

1. 每轮回调读取的 `canary.magic == timerMagic(seed,gen,id)` 且 aux canary 正确；任何失配立即 FAIL；
2. 回调的 CAS 只允许 `pending→fired`；若 `Stop()` 返回 true 后回调仍执行（state 已为 stopped）即 FAIL；
3. 每轮快照满足 `fired + stopped + pending == created`，且 `pending >= longN`（1h timer 在 Stop 前必未到期）；
4. 每轮收尾满足 `fired + stopped == created`、`pending==0`、registry 长度 0、`stop_true == stopped`；
5. `long_fired == 0`（1h timer 不可能到期）；
6. 全局 `timers_created == fired + stopped`、`stop_true == stopped`、registry 为空；
7. 固定轮数模式每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started == finished`；
8. 收尾后 goroutine 回基线（+8 容差，容忍刚返回的回调 goroutine）；
9. 最后一行 `STRESS PASS u11_timer_heap_churn`。

# 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 8 | worker 数（≤32） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | magic/随机种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 16 | `seconds=0` 时每 worker 轮数（每轮 4096 个 timer） |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

每轮分类固定：long = 128、medium = 512、short = 3456（可被代码内常量调整）。

# 失败判读

- `canary mismatch ...` / `aux canary ...`：timer 闭包捕获对象被提前回收或写坏（UAF）；
- `callback ran but state=...`：Stop 返回 true 后仍执行回调（timer 语义错误）；
- `self-delete failed: entry missing`：registry 条目丢失/重复删除；
- `Stop()=true but state=...`：Stop 成功与回调并发冲突；
- `1h timer Stop()=false` / `long_fired>0`：长周期 timer 提前到期；
- `fired+stopped+pending != created` 或 round-final 不一致：到期/停止/未到期计数丢失或重复；
- `registry len=... want=0`：回调自删除泄漏；
- `no progress`：watchdog 超时（timer 堆/调度卡死），附全栈；
- `goroutine leak`：收尾后仍有回调/辅助 goroutine；
- 只在 clobberfree/soak 下失败：UAF/提前回收；base 也失败多为计数/状态机逻辑错误。

# 运行

```bash
SUITE=stress FILTER=u11 STRESS_SECONDS=60 PROFILES=soak,soak-single ./run.sh
SUITE=stress FILTER=u11 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=u11 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 PROFILES=base ./run.sh
```
