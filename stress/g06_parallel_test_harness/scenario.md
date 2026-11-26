---
id: g06_parallel_test_harness
priority: P0
covers: [L02, L17, L18]
requires: []
default_seconds: 60
max_workers: 512
---

# 场景

并行测试 harness：issue1 的**载体复刻**——cel-go `TestInterpreter` 对大量子测试 `t.Parallel()`，并发 goroutine 同时执行 `program() → checker.Check() → ast.NewCheckedAST()`，GC 在扫描窗口命中 `NewCheckedAST` 帧 → `fatal error: invalid pointer found on stack`。

- **数百个子测试 goroutine 并发跑同一深调用链**：每波 `STRESS_WORKERS`（默认 96）个 case 并发执行 `programCase`：buildAST（AST 构造，depth 链）→ `checkRec`（checker.Check 的 AST 深遍历，深调用 → 浅返回锯齿）→ `checkLike`（NewCheckedAST 同构：map 构造/迭代、非常量下标局部数组、返回前取址 `&earr[m]` 存进结果）→ `evalRec`（再深遍历）——每个 case 带独立小状态对象 `caseState`（3 张 map + issues + 自己的 seed/base），每子测试独立断言；
- **波栅栏 shrink 放大**：整波 case 在浅栈点（checkLike 内 gate）集合，gate driver 把一批 pending 合并成**一次** `runtime.GC()` 后放行全部——一次 GC 同时扫/缩 N 个 case 的深栈，`copystack → adjustpointers` 反复扫描 checkLike 帧（issue1 崩溃路径 + 并发放大）；
- **GC worker 独立高并发**：gcChurn goroutine 常驻高频 GC；`STRESS_GMX=8,16` 阶段切换 GOMAXPROCS（高值阶段 GC 后台 mark worker 多，issue1 §2 的 `GOMAXPROCS=32` 放大器同思路）；
- **go test 生命周期**：每波整波并发启动（t.Parallel 同构），`WaitGroup` + 超时控制（`STRESS_CASE_TIMEOUT`，go test -timeout 同构），波间串行、波内并发。

失败判定以**进程崩溃本身**为准（fatal error 时 `failf` 到不了，`STRESS_CRASH_FILE` 落盘留现场）：`runtime: bad pointer in frame ... / invalid pointer found on stack`（issue1 现场原样）即 FAIL。

## 与 issue1 的对应关系（issue/issue1.md）

- §1 被扫帧 `NewCheckedAST` 与栈形 `tRunner → TestInterpreter.func1 → program → checker.Check → NewCheckedAST`：本场景 `programCase → checkRec（深遍历）→ checkLike（NewCheckedAST 同构，含 map 迭代/非常量下标局部数组/取址存结果）` 逐段同构；
- §2 复现特征「对大量子测试调用 t.Parallel()，并发 goroutine 同时执行同一深调用链」：波内 N 个 case 并发 + 波栅栏让一次 GC 同时扫全部深栈；
- §2 放大命令 `GOMAXPROCS=32`：STRESS_GMX=8,16 阶段切换（GC worker 多）；
- §4 崩溃路径 `scanstack → shrinkstack → copystack → adjustpointers`：波栅栏 GC 精确制造（case 深遍历刚返回、浅栈阻塞，满足 shrinkstack 条件）；
- §2 「崩溃与业务断言无关，进程被运行时直接 abort」：本场景失败判读同样以进程崩溃为准。

## 不变量

1. `checkRec`/`evalRec` 链 checksum = `(depth+1)*base + depth*(depth+1)/2`，帧槽展开路径逐层校验；
2. 波栅栏恢复后（shrink/copystack 之后）：`picked`、`ret.local`、`types`/`exprTypes` 抽样、`issues`、`srcInfo` 全部 canary/哨兵正确——任何栈搬移漏重定位立即 FAIL；
3. 抽样子测试（id%16==0）weak 强引用断言：`ret.local` 仍被强引用时 `weak.Value() != nil`；
4. 每波 case 全数完成（`case timeout` 或 `cases returned error` 即 FAIL）；`cases_done == waves × cases_per_wave`（固定波数模式）；
5. `gate_gc > 0`（波栅栏确实工作）；心跳持续增长（每波一拍），watchdog 6 个周期无进展 → FAIL（附全栈）；
6. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
7. 最后一行 `STRESS PASS g06_parallel_test_harness`——官方 Go 栈图精确、恒 PASS；**崩溃 = 编译器 bug**（栈图把非指针槽当指针），PASS = 本轮未触发。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定波数 |
| `STRESS_WORKERS` | 96 | 每波子测试数（≤512） |
| `STRESS_DEPTH` | 384 | 深遍历递归深度（64..4096） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 确定性 ID/种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 10 | `seconds=0` 时波数 |
| `STRESS_GMX` | 空 | 阶段边界切换 GOMAXPROCS（如 `8,16`）；空=尊重 profile/环境 |
| `STRESS_CASE_TIMEOUT` | 120 | 每波超时（秒），go test -timeout 同构 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径（fatal error 现场） |

`.skip-envs` 含 `detector`：`efence=1` 逐对象 page 化与每波数百 case 的分配量不合，且栈扫描判据不依赖 efence。

## 失败判读

- 进程崩溃（`runtime: bad pointer in frame ... / invalid pointer found on stack`）：**shrinkstack 的 adjustpointers 按栈图扫到非指针值**（issue1 现场原样）——直接 FAIL（run.sh 按非零退出 + crash 文件判定）；
- `frame slot corrupted` / `checksum=... want=...`：深遍历返回路径或栈搬移损坏；
- `post-gate.* canary mismatch`：shrink/copystack 漏重定位（最敏感的非崩溃信号）；
- `case timeout after ...` / `cases returned error`：go test 生命周期失败（case 卡死或校验错误）；
- `strong ref held but object collected`：weak 强引用断言失败——漏标判据；
- `no gate GC happened` / `no waves completed`：场景结构退化；
- `no wave progress`：watchdog 超时（driver/churn/GC 卡死），附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- 只在 window/soak/clobberfree 下失败：栈扫描 UAF；base 也失败多为 checksum/控制流逻辑错误。

## 运行

```bash
SUITE=stress FILTER=g06 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=g06 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 STRESS_GMX=8,16 PROFILES=base ./run.sh
SUITE=stress FILTER=g06 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=g06 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=2 PROFILES=base ./run.sh
```

## 来源

- `issue/issue1.md` §1（崩溃帧/栈形/路径）、§2（t.Parallel 并发形态/GOMAXPROCS 放大/与断言无关的 abort）、§4（现场坏指针）；
- `stress/g02_shrink_badpointer`（checkLike/checkRec/evalRec 同构链——g06 是其 t.Parallel 并发载体）、`stress/u09_stack_shrink_cycles`（浅栈阻塞 + driver GC 的收缩触发）、`stress/s04_preempt_storm_soak`（STRESS_GMX 阶段切换）；
- `stress/x01_aggregate_statepoint`（harness 骨架：canary/weak/poison/心跳/watchdog）。
