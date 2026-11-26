---
id: x07_cgo_panic_deepstack
priority: P0
covers: [L09, L05, L17, L04]
requires: [cgo]
default_seconds: 60
max_workers: 8
---

# 场景

cgo 回调内的 panic/深栈/GC assist 长跑（需 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`，C 定义在 `x07_cgo.c`）。

C 侧每次风暴创建 1–2 个 pthread，每线程反复把 `//export goCbPanic` 作为函数指针回调 Go；回调返回后 C 线程继续下一次回调。回调内按轮换模式执行：

- `mode=0`：`deepRun` 递归 2048 层（每层 `local *node` + `[4]*node` 指针槽），叶子 `runtime.GC()` 后逐层校验；
- `mode=1`：同 mode 0，叶子 GC 后 `panic(*deepPayload)`；panic payload 持叶子帧指针，回调帧内 `recover`，recover 后再 GC 并校验 payload 指向对象未被回收/改写；
- `mode=2`：同 mode 1，另外每 256 层的 `defer` 在 panic 展开期间触发 `runtime.GC()` 并复查该帧指针槽（panic 展开 × GC 窗口）；
- `mode=3`：连续两轮 `panic → recover`（各一次 GC），两段都在返回 C 之前 recover。

`runtime.Goexit` 未作实现：Goexit 会终止回调 goroutine、无法把返回值交还 C pthread，C 侧会永久挂起；按调研条目约定改用上述“深层 panic + recover / 展开期 GC”变体（进程不崩溃）。

Go 侧 governor 每 250 ms 读 `HeapAlloc` 并把 `debug.SetMemoryLimit` 动态设为 live heap 的 1.1/1.25/1.5 倍（下限 8 MiB），把回调递归中的分配持续推入 GC assist；收尾恢复原 GOMEMLIMIT。`/cpu/classes/gc/mark/assist:cpu-seconds` 与 `NumGC` 只记录不断言。

C 侧累计回调返回值并返回校验和/回调次数；Go 侧用纯函数 `cbExpected/expectedStorm` 独立计算期望值。`runtime.NumCgoCall()` 只记录。

## 不变量

1. 每次风暴的 Go 回调增量 = C 返回 `out_calls` = `nthreads*iters`，按 base 槽精确校验；
2. C 侧累计校验和 = Go 独立期望值（覆盖 4 种模式与深递归闭式值）；
3. 递归每层 `local/pad[0]/pad[3]` 在 GC 后内容不变；叶子帧在 GC 前后均正确；
4. panic payload 中的帧指针跨 panic 展开与 recover 后 GC 仍指向内容正确的对象；
5. mode=2 的展开期 defer 中，帧指针槽在 GC 前后保持正确；
6. 回调内局部 buffer 跨回调内 GC 保持内容正确（C→Go 回调帧可被扫描）；
7. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；心跳持续增长、watchdog 无停滞；
8. 收尾 quiesce 后 goroutine 数不超过启动基线 + 8；
9. 最后一行 `STRESS PASS x07_cgo_panic_deepstack`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 2 | worker 数（≤8） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `STRESS_FIXED_ROUNDS` | 8 | `seconds=0` 时每 worker 轮数 |
| `UAF_SEED` | 1 | 模式/线程数/迭代数种子，失败输出带出 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

场景常量：递归深度 2048、每风暴 C 线程 ≤2、每线程 3–6 次回调。

## 失败判读

- `phase=preflight mode=...`：`//export` 签名与 C 声明不一致、回调未链接或 panic/recover 路径不可用；
- `phase=storm ... callbacks/out_calls=`：C 线程数/回调次数丢失，pthread join 或函数指针异常；
- `phase=storm ... checksum=`：回调参数/返回值被破坏（cgo 调用约定、寄存器保存或栈扫描问题）；
- `phase=callback-deep ... frame corrupted`：深递归帧/指针槽在 GC 后损坏（栈映射或栈复制错误）；
- `phase=unwind-gc ... frame corrupted`：panic 展开期间的帧/指针被 GC 破坏；
- `phase=panic-payload/panic-recover-gc ...`：panic payload 或其中帧指针在展开/回收后失效；
- `phase=callback ... local buffer corrupted`：回调帧局部变量被破坏；
- `watchdog stalled`：回调/gc assist 死锁或 C 调用未释放 P；
- 构建失败：先确认 `CGO_ENABLED=1`、gcc 可用、`BUILD_MODE=module`；
- 只在 window（`gcstoptheworld=2,gccheckmark=1`）失败：漏标/栈映射问题优先级最高。

## 运行

```bash
CGO_ENABLED=1 SUITE=stress FILTER=x07 STRESS_SECONDS=60 PROFILES=soak ./run.sh
CGO_ENABLED=1 SUITE=stress FILTER=x07 STRESS_SECONDS=3 PROFILES=soak,window REPEAT=1 TIMEOUT=120 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
CGO_ENABLED=1 SUITE=stress FILTER=x07 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
CGO_ENABLED=1 SUITE=stress FILTER=x07 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 BUILD_FLAGS=-race PROFILES=base ./run.sh
# 手动构建：
cd stress/x07_cgo_panic_deepstack && CGO_ENABLED=1 go build -o /tmp/x07 . && /tmp/x07
```

本场景不进默认 CI（`requires: [cgo]`）；`CAPS` 不含 `cgo` 时 runner 自动跳过。

## 来源

- `docs/research/stress_extreme_survey.md` x07（cgo 回调 × 深栈/panic × GC assist/GOMEMLIMIT）
- `go/src/runtime/testdata/testprogcgo/callback.go`（C 多线程回调 Go、回调内 GC/深递归）
- `go/src/runtime/crash_cgo_test.go`（cgo 回调/panic 路径）
- `go/src/runtime/panic.go`、`mgcmark.go`、`mbarrier.go`（panic 记录、栈扫描、写屏障）
- n07_cgo_storm（回调风暴外壳；本场景新增 panic/展开期 GC/GOMEMLIMIT assist）
