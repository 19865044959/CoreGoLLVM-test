---
id: x04_lockosthread_stw
priority: P1
covers: [L04, L17]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

LockOSThread × 线程退出（oldm）× STW × 栈增长/收缩的叠加风暴。s03/u09 只打纯栈增长/收缩，u07 打普通 goroutine 退出；本场景新增 **locked 线程** 维度：

- worker 每轮 `runtime.LockOSThread()` → 深递归（默认 384 层，每帧 16 个指针槽；栈底与每 64 层显式 `runtime.GC()`，制造 morestack/STW 交错）→ 浅递归 16 轮 → GC 后校验 → `UnlockOSThread()`；
- 另起 goroutine：`LockOSThread()` 后构造 canary 指针数组并做一次 GC，再 `runtime.Goexit()` 且**不 Unlock**——按 runtime 语义 goroutine 退出即 OS 线程退出（oldm 路径）；defer 链在退出过程中再 GC，校验全量 canary 后把校验和交回父 goroutine；
- 观察者 goroutine 周期 `runtime.ReadMemStats`（每次短暂 STW）；
- 独立 GC churn goroutine 持续 `runtime.GC()`；
- 每轮对 strong-reachable 对象做 `weak.Make` 存活探针（GC 后 `Value()` 必须非 nil）；
- `debug.SetMaxStack(64MiB)`；递归深度受 `STRESS_DEPTH` 限制且预检估算用量，防真溢出。

判据全部是确定性 canary / weak 探针 / 计数一致性，不做墙钟吞吐断言；所有 `time.Sleep` 仅用于节奏控制。

来源：`go/src/runtime/stack_test.go`、`proc_test.go`（locked goroutine/线程退出）、`testprog/lockosthread.go`、`stw_mexit.go`；调研文档 x04 与 §3.2（window/detector profile）。

## 不变量

1. 深递归每帧 `sum == Σ nodeMagic(seed, baseID+i)`；返回途中重新读取的帧内指针（slot0/slotParent/slotLast）未被改写；
2. 每层 GC 点前后 canary 一致（UAF/漏标即 FAIL）；
3. 每轮 `weak.Make(anchor).Value() == anchor`（持强引用且 GC 后仍可见）；
4. `LockOSThread` 次数 == `UnlockOSThread` 次数 == 已完成轮数；
5. exited locked goroutine：`spawned == finished == rounds × STRESS_EXITS`，defer 内 GC 后的校验和与期望公式一致；
6. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；
7. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
8. 最后一行 `STRESS PASS x04_lockosthread_stw`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤32） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | seed，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 8 | `seconds=0` 时每 worker 轮数 |
| `STRESS_DEPTH` | 384 | 深递归层数（16–4096） |
| `STRESS_EXITS` | 2 | 每轮 locked-Goexit goroutine 数（1–16） |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `canary ... (UAF/栈扫描漏标)`：locked 线程上的栈帧/栈搬移扫描错误，或对象被提前回收；
- `帧内指针被改写` / `sum ... want ...`：栈复制/收缩时指针重定位错误；
- `weak.Value()=nil`：强引用对象被错误回收（漏标）；
- `LockOSThread=... UnlockOSThread=...`：场景自身锁计数失衡（子 goroutine panic 等）；
- `exit goroutines spawned != finished`：locked goroutine 退出路径卡死/校验和未交回；
- `6 个心跳周期无进展`：线程退出或 STW 路径死锁，附全栈；
- `goroutines baseline ... leak`：收尾泄漏；
- 只在 `window`（`gcstoptheworld=2,gccheckmark=1`）失败：漏标/并发窗口问题；`detector`（efence）短跑崩溃：栈/对象释放即 fault。

## 运行

```bash
SUITE=stress FILTER=x04 STRESS_SECONDS=3 PROFILES=soak,window REPEAT=1 TIMEOUT=90 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=x04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree REPEAT=1 TIMEOUT=120 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=x04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 BUILD_FLAGS=-race PROFILES=base REPEAT=1 TIMEOUT=300 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
```

## 来源

- `go/src/runtime/stack_test.go`（栈增长/收缩与 GC 交互）、`proc_test.go`（locked goroutine / 线程生命周期）；
- `go/test/testprog/lockosthread.go`（约 2% 命中的 LockOSThread 历史问题）、`stw_mexit.go`（STW 与 oldm 退出）；
- `docs/research/stress_extreme_survey.md` x04 条目与 §3.2（window/detector/layout profile）；R §2 栈/线程风险点。
