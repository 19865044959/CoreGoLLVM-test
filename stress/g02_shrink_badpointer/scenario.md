---
id: g02_shrink_badpointer
priority: P0
covers: [L04, L17, L18]
requires: []
default_seconds: 60
max_workers: 8
---

# 场景

栈收缩扫坏指针：issue1（cel-go ARM64 偶现 `fatal error: invalid pointer found on stack`，崩溃帧 `common/ast.NewCheckedAST`，路径 `scanstack → shrinkstack → copystack → adjustpointers`）的**核心复刻**。把触发形状做成可长跑、可穷举扫描的形态：

- **深调用 → 浅返回锯齿**：每个并发 worker 每轮先做 `checkRec`（checker.Check 的 AST 深遍历同构，`STRESS_DEPTH` 默认 512 层，栈随 morestack 长高），返回后留在浅栈执行 `checkLike`（NewCheckedAST 同构）——与 issue1 现场栈形 `tRunner → TestInterpreter.func1 → program → checker.Check → NewCheckedAST` 一致；
- **NewCheckedAST 同构**（崩溃帧形状）：中等复杂度函数，指针局部（3 张 `map[int64]*typeInfo` 构造 + 迭代）、多返回值 `(ret *checkedAST, err error)`、defer、**非常量下标局部数组**（`m = (seed+base)%4`：栈驻留 `arr[4]*typeInfo` 经 `arr[m]` 值拷贝 + 逃逸 `earr[4]typeInfo` 返回前取址 `&earr[m]` 存进结果对象——模拟 checker.Check → ast.NewCheckedAST 链）；
- **shrink 高频触发**：worker 在浅栈点经 gate 阻塞，专用 shrink driver 执行 `runtime.GC()`（used < 1/4 alloc → shrinkstack → copystack → adjustpointers 反复扫描 checkLike 帧），独立 gcChurn goroutine 持续分配 + GC 加频（issue1 §2 的并发 GC 放大）；锯齿后半段 `evalRec` 再深遍历重新长高，循环往复；
- **GOMAXPROCS 高值阶段**：`STRESS_GMX=2,8,16` 阶段切换（高值阶段 GC 后台 mark worker 多，issue1 §2 的 `GOMAXPROCS=32` 放大器同思路）；未设置时尊重 profile/环境。

失败判定以**进程崩溃本身**为准（fatal error 时 `failf` 到不了，`STRESS_CRASH_FILE` 落盘留现场）：`runtime: bad pointer in frame ... / invalid pointer found on stack`（issue1 现场原样）即 FAIL。window/soak 双跑 + soak-single。

## 与 issue1 的对应关系（issue/issue1.md）

- §1 崩溃现场：`runtime.adjustpointers → adjustframe → copystack → shrinkstack → scanstack → markroot`——本场景的 shrink driver GC 精确制造这条路径（worker 深栈返回后浅栈阻塞，满足 shrinkstack 条件）；
- §1 被扫帧 `NewCheckedAST`：本场景 `checkLike` 同构复刻其全部形状要素（中等复杂度、指针局部、多返回值、defer、非常量下标局部数组、返回前取址存结果）；
- §2 复现特征：并发 goroutine 同时执行同一深调用链 + GC 在扫描窗口命中（本场景多 worker × churn × driver 三方叠加）；`GOMAXPROCS` 高值放大（STRESS_GMX）；
- §4 现场坏指针 `0xa6`（小整数）：checkLike 帧内刻意保留 map 迭代 key（int64）、下标 `m`、长度值等小整数槽与指针槽相邻的代码形状，任何栈图误标都立即变 fatal。

## 不变量

1. `checkRec`/`evalRec` 链 checksum = `(depth+1)*base + depth*(depth+1)/2`（AST 链 id 和），深遍历返回路径帧槽逐层校验；
2. gate 阻塞点恢复后（shrink/copystack 之后）：`picked`、`ret.local`（`&earr[m]`）、`types`/`exprTypes` 抽样、`issues`、`srcInfo` 全部 canary/哨兵正确——任何栈搬移漏重定位立即 FAIL；
3. weak 强引用断言：`ret.local` 仍被强引用时 `weak.Value() != nil`（对象被提前回收 = 漏标/UAF）；
4. `shrink_gc > 0`（收缩路径确实发生）；固定轮数时总轮数 = workers×phases×`STRESS_FIXED_ROUNDS` 均分；`started == finished`；
5. 心跳持续增长（仅 worker 喂心跳，churn 不掩蔽停滞），watchdog 6 个周期无进展 → FAIL（附全栈）；
6. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
7. 最后一行 `STRESS PASS g02_shrink_badpointer`——官方 Go 栈图精确、恒 PASS；**崩溃 = 编译器 bug**（栈图把非指针槽当指针），PASS = 本轮未触发。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤8） |
| `STRESS_DEPTH` | 512 | 深遍历递归深度（64..4096） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 确定性 ID/种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 20 | `seconds=0` 时每 worker 轮数 |
| `STRESS_GMX` | 空 | 阶段边界切换 GOMAXPROCS（如 `2,8,16`）；空=尊重 profile/环境 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径（fatal error 现场） |

`.skip-envs` 含 `detector`：`efence=1` 逐对象 page 化与本场景深递归 + 高频 churn 不合，且栈扫描判据不依赖 efence。

## 失败判读

- 进程崩溃（`runtime: bad pointer in frame ... / invalid pointer found on stack`）：**shrinkstack 的 adjustpointers 按栈图扫到非指针值**（issue1 现场原样）——直接 FAIL（run.sh 按非零退出 + crash 文件判定）；
- `frame slot corrupted` / `checksum=... want=...`：深遍历返回路径或栈搬移损坏；
- `post-gate.* canary mismatch`：shrink/copystack 漏重定位（本场景最敏感的非崩溃信号）；
- `strong ref held but object collected`：weak 强引用断言失败——漏标判据；
- `no shrink GC happened`：shrink driver 未工作（场景结构退化）；
- `no worker progress`：watchdog 超时（driver/churn/GC 卡死），附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- 只在 window/soak/clobberfree 下失败：栈扫描 UAF；base 也失败多为 checksum/控制流逻辑错误。

## 运行

```bash
SUITE=stress FILTER=g02 STRESS_SECONDS=60 PROFILES=soak,window,soak-single ./run.sh
SUITE=stress FILTER=g02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=50 STRESS_GMX=2,8,16 PROFILES=soak ./run.sh
SUITE=stress FILTER=g02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=g02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 PROFILES=base ./run.sh
```

## 来源

- `issue/issue1.md` §1（崩溃路径与被扫帧 NewCheckedAST）、§2（并发放大/GOMAXPROCS）、§4（现场坏指针 0xa6）；
- `stress/u09_stack_shrink_cycles`（shrink driver/gate 阻塞收缩的确定性触发骨架）、`stress/x02_localarray_liveness`（非常量下标局部数组 + 取址跨 call 形状）、`stress/s04_preempt_storm_soak`（STRESS_GMX 阶段切换）；
- `stress/x01_aggregate_statepoint`（harness 骨架：canary/weak/poison/心跳/watchdog）。
