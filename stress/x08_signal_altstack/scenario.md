---
id: x08_signal_altstack
priority: P1
covers: [L09, L19, L17, L04]
requires: [cgo]
default_seconds: 60
max_workers: 8
---

# 场景

信号 altstack / sigpanic / C 换栈回调长跑（需 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`，C 定义在 `x08_cgo.c`）。

C 侧每次风暴创建 1–2 个 pthread；偶数线程先用 `sigaltstack` 安装 64 KiB 用户信号栈（奇数线程不装，覆盖 issue #22930 两条路径），再回调 `//export goSigCb`；回调返回后恢复信号栈并对旧栈 `mprotect(PROT_NONE)` 防复用。回调子模式：

- `sub=0`：分配 node / 局部 buffer，约每 64 次回调触发一次 `runtime.GC()`，校验内容；
- `sub=1`：24 层嵌套 Go 帧（每层指针槽）后对 nil 指针解引用 → SIGSEGV → runtime `sigpanic` 在当前 gsignal/altstack 上展开 Go 帧 → 回调帧内 `recover`，校验 panic 为 `runtime.Error` 且含 “invalid memory address”，recover 后再 GC 并复查回调局部 buffer；
- `sub=2`：`debug.SetPanicOnFault(true)` 后对恒不可映射的**非 nil** 地址（`uintptr(1)`）取值 → sigpanic → recover 校验，返回前恢复 `SetPanicOnFault` 原值；
- `sub=3`：C 侧用 glibc `ucontext`（`getcontext/makecontext/swapcontext`）换到 64 KiB 自建栈后回调 Go（对应 Go `testprogcgo.StackSwitchCallback`），回调内 `runtime.GC()` + 内容校验，再换回原栈。每个换栈线程先在普通 pthread 栈上做一次预热回调（`iter=-1`，不计数），让 runtime 记录该 C 线程的栈边界。

独立 C pthread 持续向进程发 `SIGURG`（异步抢占）+ `SIGPROF`（profiling 信号）洪水（约 4k 次/秒），洪水线程自阻塞这两个信号避免自打断；Go runtime 的信号处理器在 gsignal/用户 altstack 上运行。

**平台说明**：
- `sub=3` 依赖 glibc `ucontext`；musl（非 glibc linux）编译时 `c_stack_switch_run` 返回 `-4`，场景自动关闭 sub=3（metrics `stack_switch_ok=0`），其余子模式继续。
- 在 stock Go 1.24.1/linux-amd64 上，“SIGURG+SIGPROF 双洪水 + ucontext 换栈回调”同时发生会 hang（两种信号各自单独与换栈组合均稳定）。因此换栈风暴前短暂 `c_flood_pause()`（再留 200 µs 排空在途信号），换栈结束后 `c_flood_resume()`；双信号洪水与 altstack/sigpanic 子模式仍然重叠运行。若移植到其他平台后 window profile 仍不稳，把 `window` 加入本目录 `.skip-envs` 并在此注明。

C 侧累计回调返回值并返回校验和/次数；Go 侧 `subExpected/expectedStorm` 独立计算期望值。`runtime.NumCgoCall()` 只记录。

## 不变量

1. 每次风暴 Go 回调增量 = C 返回 `out_calls` = `nthreads*iters`，按 base 槽精确校验；
2. C 侧累计校验和 = Go 独立期望值（4 种子模式）；
3. nil deref 与 `SetPanicOnFault` 两条 sigpanic 路径都必须真实触发并被 recover 校验（`nil_panics/fault_panics > 0`）；
4. sigpanic recover 后再次 GC，回调局部 buffer 内容不变；
5. 用户 altstack 上的回调与 GC 正常，恢复信号栈后不产生二次信号栈复用；
6. 换栈回调中 GC 后 node 内容不变；恢复原栈后线程继续正常回调；
7. 信号洪水线程持续发送 SIGURG/SIGPROF（`flood_signals > 0`），进程不崩溃/不挂起；
8. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；心跳持续增长、watchdog 无停滞；
9. 收尾 quiesce 后 goroutine 数不超过启动基线 + 8；
10. 最后一行 `STRESS PASS x08_signal_altstack`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 2 | worker 数（≤8） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `STRESS_FIXED_ROUNDS` | 6 | `seconds=0` 时每 worker 轮数 |
| `UAF_SEED` | 1 | 子模式/线程数/迭代数种子，失败输出带出 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

场景常量：故障嵌套 24 层 Go 帧；每风暴 C 线程 ≤2；换栈风暴每线程 2–4 次回调，普通风暴 4–8 次。

## 失败判读

- `phase=preflight sub=...`：C 回调未链接、sigaltstack 安装失败或 sigpanic 未按预期触发；
- `phase=preflight-stackswitch ...`：ucontext 换栈路径不可用（非 -4 的 C 错误码）；
- `phase=sigpanic ... fault did not panic`：故障地址被映射/nil 优化消失，或信号处理器未转成 panic；
- `phase=sigpanic ... unexpected panic type/error`：sigpanic 路径被破坏（不是 runtime.Error）；
- `phase=sigpanic-gc ... local buffer corrupted`：sigpanic 展开/恢复后回调帧被回收/改写；
- `phase=storm ... checksum/callbacks/out_calls`：回调参数/返回值或计数被破坏；
- `phase=stack-switch ... local node corrupted`：换栈后 Go 回调的栈扫描/GC 异常；
- `phase=final: ... path did not run`：某个子模式未被覆盖（种子/平台导致）；
- `watchdog stalled` 或超时 124：信号洪水/换栈/GC 死锁（优先看是否触发平台组合问题）；
- 构建失败：确认 `CGO_ENABLED=1`、gcc 可用、glibc（ucontext）。

## 运行

```bash
CGO_ENABLED=1 SUITE=stress FILTER=x08 STRESS_SECONDS=60 PROFILES=soak ./run.sh
CGO_ENABLED=1 SUITE=stress FILTER=x08 STRESS_SECONDS=3 PROFILES=soak,window REPEAT=1 TIMEOUT=120 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
CGO_ENABLED=1 SUITE=stress FILTER=x08 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
CGO_ENABLED=1 SUITE=stress FILTER=x08 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 BUILD_FLAGS=-race PROFILES=base ./run.sh
# 手动构建：
cd stress/x08_signal_altstack && CGO_ENABLED=1 go build -o /tmp/x08 . && /tmp/x08
```

本场景不进默认 CI（`requires: [cgo]`）；`CAPS` 不含 `cgo` 时 runner 自动跳过。

## 来源

- `docs/research/stress_extreme_survey.md` x08（gsignal/altstack 上的 sigpanic、换 C 栈、外部 SIGPROF）
- `go/src/runtime/signal_unix.go`（sighandler/sigpanic/gsignal、sigaltstack 处理）
- `go/src/runtime/testdata/testprogcgo/sigstack.go`（C 线程自建信号栈后回调 Go）
- `go/src/runtime/testdata/testprogcgo/stackswitch.go` + `stackswitch.c`（ucontext 换栈回调，issue #62440）
- `go/src/runtime/crash_cgo_test.go`（TestSigStackSwapping、TestStackSwitchCallback）
- n06_exec_signal_lifecycle / n07_cgo_storm（信号/cgo 外壳；本场景新增 altstack/sigpanic/换栈）
