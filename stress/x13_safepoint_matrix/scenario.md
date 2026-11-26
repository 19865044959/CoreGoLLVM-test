---
id: x13_safepoint_matrix
priority: P0
covers: [L17, L18, L05]
requires: []
default_seconds: 60
max_workers: 12
---

# 场景

safepoint 矩阵（P0，方法论核心场景）：把“偶现 UAF”转化为“对每个 step 边界必测一次”的有限可枚举注入，覆盖率可度量、失败可回放。现有 30 个场景的 GC 都在阶段/轮边界，x13 用操作脚本 + 穷举注入点替代随机长跑。

**12 个 shape**（每个代表一种 IR 形态）：

| # | shape | 形态 |
|---|---|---|
| 0 | `fca_args` | 聚合按值传参/返回：`[2]*T`、`struct{A [2]*T; B [2][2]*T}`、int/string/iface 混排 |
| 1 | `local_array_addr` | 局部数组取址：`&arr[非常量下标]` 跨 noinline 调用，返回后重读 |
| 2 | `closure_capture` | 闭包捕获指针：创建/调用/组合/改写捕获变量 |
| 3 | `deep_recursion` | 16–24 层递归，每帧持指针与 `[4]*T` 槽 |
| 4 | `defer_chain` | 4 层 defer 链、命名返回被 defer 改写、panic/recover 展开 |
| 5 | `iface_dispatch` | 两个实现的动态派发、type switch、method value、`any` 断言 |
| 6 | `generic_inst` | `[2]*T` / struct / `any` / `~*T` 约束的泛型实例化 |
| 7 | `chan_handoff` | 缓冲 chan 交接指针（GC 时指针只存在于 channel buffer） |
| 8 | `unsafe_interior` | `unsafe.Pointer` / 强类型内部指针；holder 仅由 `&pad[1]` 保持可达 |
| 9 | `large_byval` | >128B 大 struct（含指针/string/iface）按值传入/返回 |
| 10 | `namedret_rvo` | 命名返回 + RVO 大 struct，defer 改字段，multi-return |
| 11 | `tailcall_wrapper` | 小 wrapper 链尾调用 noinline work、method value、interface、deferreturn |

**注入矩阵**：每个 shape 的操作序列拆成 5 步（每步一个 closure，底层为 `//go:noinline` step 方法）。6 个边界（step 0 前、每步之后）= 单点 6 个；有界双点 `i<j` 共 C(6,2)=15 个；每个点独立重建 state 后运行。总点数 = 12 × 21 = **252 点/矩阵**，实际注入 GC 432 次/矩阵（双点两次）。

- 每个注入点调用 `runtime.GC()`（唯一 GC 来源，无后台 GC churn 干扰）；
- `runtime.GOMAXPROCS(1)` 强制串行；每个注入点再加全局锁，保证任意时刻只有一个 shape 在跑，GC 点严格等于枚举点；
- 每步后校验全部 anchor 的 canary 与 `weak.Pointer`（持强引用时 `Value()==nil` 即 FAIL），GC 注入后也立即校验；
- 失败行固定格式：`FAIL: shape=<name> step_i=<i> step_j=<j> seed=<n> gcCount=<n> round=<n> ...`，可精确重放；
- metrics 打印 `injection_points_covered / injection_points_total`；秒数模式下至少完成一个完整矩阵、固定轮数下矩阵数必须等于 `STRESS_FIXED_ROUNDS`，未扫完不得 PASS。

`window` profile（`GOGC=off` + `gcstoptheworld=2` + `gccheckmark=1` + `clobberfree=1`）下最狠：每次 GC 全 STW、同步 sweep、checkmark 二次标记，任意漏标即 panic，且排除后台并发标记与窗口重叠。

## 不变量

1. 每个 state 的全部 anchor 在任意注入点 GC 后 canary 不变（`v == mnodeV(seed,id)`）；
2. 持强引用（state 字段/局部）时 `weak.Value()` 必须非 nil；“活着被回收”立即 FAIL；
3. 每个 shape 的 5 步操作结果自洽：指针身份（chan FIFO、unsafe 内部指针、闭包写入、RVO defer 修改）与返回值/累加器符合确定性序列；
4. `injection_points_covered == injection_points_total`，且 `gc_injections == gc_want`（每次注入都按枚举执行）；
5. 固定轮数 = 完整矩阵数，每 worker 恰好 `STRESS_FIXED_ROUNDS` 个矩阵；秒数模式至少 1 个完整矩阵；
6. `started == finished`，收尾 goroutine 回基线（+4 容差）；
7. 最后一行 `STRESS PASS x13_safepoint_matrix`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数（轮=完整矩阵） |
| `STRESS_WORKERS` | 1 | worker 数（≤12，按 shape 取模分片；默认 1 保证严格确定性） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 种子，失败行带出 |
| `STRESS_FIXED_ROUNDS` | 2 | `seconds=0` 时完整矩阵数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

失败行格式固定为 `shape / step_i / step_j / seed / gcCount`：

- `step_j=-1` 表示单点注入；`step_j>=0` 表示双点 `i<j`；
- canary 失配 / `nil *mnode`：GC 在枚举点回收了仍被 shape 引用的对象（stackmap/聚合/frames 漏根）；
- `weak.Value()==nil while strongly anchored`：活对象被回收的直接判据（不依赖内存复用）；
- `unsafe.ipp` 失配：仅由内部指针保持可达的 holder 被错误回收；
- `chan: FIFO 顺序错误` / `身份错误`：channel buffer 中的指针被 GC 破坏；
- `injection_points_covered != total`：矩阵未扫完（seconds 到期或提前退出），不得 PASS；
- `gc_injections ... want ...`：注入点遗漏/重复；
- `no injection progress`：watchdog 超时；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- 只在 `window` 下失败：漏标/窗口问题（`gccheckmark` 直接 panic 可定位）；只在 clobberfree 下失败：UAF 内容破坏。

## 运行

```bash
# 推荐：window 优先，固定轮数完整扫矩阵
SUITE=stress FILTER=x13 STRESS_SECONDS=3 PROFILES=window,soak REPEAT=1 TIMEOUT=90 ./run.sh
SUITE=stress FILTER=x13 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=x13 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=2 PROFILES=base ./run.sh
```

## 来源

- `docs/research/stress_extreme_survey.md` x13、§3.1/3.2/3.3（B 级“穷举注入点”）；
- go#80188（async preempt 小帧）、go#80147（保守扫描 + SIGURG）、`runtime/extern.go:94-113,231-240`、`runtime/preempt.go:421-433`；
- jcstress 的 outcome/fault schedule 与 TigerBeetle VOPR / FoundationDB Simulation 的 seed+step 回放思想（Go 无 runtime stub，退化为操作脚本 + 穷举边界）；
- 现有 30 个场景均为随机/阶段 GC，无 `runtime.GCCheckmark` 与穷举注入，本场景与其正交。
