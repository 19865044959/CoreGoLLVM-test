---
id: z09_init_time_stackgrowth
priority: P1
covers: [L12, L17, L13]
requires: []
default_seconds: 60
max_workers: 8
---

# 场景

init 期栈增长（go#41816）：Go 1.15 回归中，init 函数里 mapassign 触发栈复制时 init 帧的指针映射错误，运行时报 `invalid pointer found on stack`。

同一包的多个 `func init()` 分布在 `init_a.go` / `init_b.go`，各自覆盖一种组合：

- **init_1（init_a.go）**：大局部帧 `[4096]*node`（32KB，超过初始栈）局部数组 + `map[int]*node` + `[]*node` 混合；填满后在函数中部调用 `deepRec`（noinline，512 层，每 64 层 GC）强制 morestack——栈复制必须正确搬运含 4096 个指针的 init 帧；增长与 GC 后逐项校验数组/map/slice canary，并把数组指针发布到全局 `init1Nodes`；
- **init_2（init_b.go）**：`map[int]*node` 不预设容量循环写入 1000 项（mapassign 触发多次扩容），每 97 次插入调用一次 `deepRec`（栈增长与 mapassign/扩容交错），每 211 次插入一次 `runtime.GC()`；结束后 GC 并全量校验 1000 项；
- **init_3（init_b.go）**：包级变量初始化链 `gA = buildA(); gB = buildB(gA); gC = buildC(gB)`（依赖顺序由 Go 初始化依赖规则保证）+ `runtime.GC()` + 跨 init 顺序校验（`init_1 → init_2 → init_3`，用 stage 标志与 `initOrder=[1,2,3]` 双重判定，`buildB/buildC` 校验前驱内容）。

init 只运行一次。main 启动时全量校验三份成果（运行次数各 1、stage/顺序、4096 数组 checksum、1000 项 map、gA→gB→gC 链），随后 worker 循环对 init 期对象做长期有效性压力：每轮分配新 `holder` 挂到本 worker 独占的 `init1Nodes[w.id].next`（init 期老对象 → 本轮新对象，考写屏障与 liveness）→ `runtime.GC()` → 读回 holder、slot、64 个抽样 init 对象、init_2 map 抽样与全局链；收尾再全量校验一次。独立 goroutine 提供 GC churn。

## 对应历史案例

- **go#41816（2020，Go 1.15 回归）**：init 函数里 mapassign 触发栈复制、init 帧指针映射错误 → `invalid pointer found on stack`。对应 init_2 的“mapassign 扩容 + 深递归 morestack 交错”，以及 init_1 的“大指针帧在深递归触发栈复制”。
- 覆盖 stress_history_bugs.md §2“init 期栈增长”缺口；u05 有 init 但无 mapassign+morestack 组合。

## 不变量

1. 三个 init 各执行一次，顺序为 `init_1 → init_2 → init_3`（stage 标志 + `initOrder`）；
2. init_1 的 4096 个局部数组元素在栈增长与 GC 后 id/canary/tag 全部正确，checksum 一致；map/slice 各 64 项正确；
3. init_2 的 1000 项 map 在扩容、交错栈增长与 GC 后全部正确，checksum 一致；
4. 包级变量链 `gA→gB→gC` 的值满足 `gB.v == gA.v^magic(gBID)`、`gC.v == gB.v^magic(gCID)`；
5. worker 每轮 `init1Nodes[w.id].next` 写入的新对象在 GC 后仍可读回（写屏障/老年代对象可写）；
6. 启动与收尾各做一次全量校验，init 期对象在长跑后仍长期有效；
7. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started==finished`；收尾 goroutine 回基线（+4）；
8. 最后一行 `STRESS PASS z09_init_time_stackgrowth`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤8，且 ≤ init1N） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 种子（失败输出；init 内容本身确定性） |
| `STRESS_FIXED_ROUNDS` | 16 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `invalid pointer found on stack` / GC 崩溃（进程级）：init 帧指针映射错误（go#41816 形态，最敏感于 init_1/init_2）；
- `phase=init1: local array[i] corrupted after stack growth` / `map[...] corrupted` / `slice[...] corrupted`：init_1 栈复制或 GC 后指针图损坏；
- `phase=init2: map[i] corrupted after grows+GC` / `deep recursion at i=... sum mismatch`：mapassign 与栈增长交错路径损坏；
- `phase=init2|init3: init_1/init_2 has not completed` / `init order=...`：init 执行顺序或次数错误；
- `phase=globalvar` / `gB|gC corrupted`：包级变量初始化依赖顺序或内容损坏；
- `phase=post-gc ... init_1[w].next lost` / `content corrupted`：init 期对象被回收或写屏障漏标；
- `phase=init-deep*: frame corrupted`：深递归帧指针槽在 morestack/GC 后损坏；
- `phase=watchdog`：init 或 GC 卡死，附全栈。

## 运行

```bash
SUITE=stress FILTER=z09 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=z09 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=z09 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 PROFILES=base ./run.sh
```

## 来源

- [go#41816 init mapassign stack copy](https://github.com/golang/go/issues/41816)
- `go/src/runtime/stack.go`（newstack / 栈复制与指针调整）、`runtime/map.go`（mapassign 扩容）
- `stress/u05_pool_identity_reuse`（init 使用现状）、`stress/z07`（zerorange 大帧思路）
