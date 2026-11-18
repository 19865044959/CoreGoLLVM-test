# Go 官方长跑/压测写法调研

调研范围：`go/test/stress/`（官方常驻 runtime 压测器）、`go/src/runtime/*_test.go`、`go/src/internal/testenv`、`go/src/runtime/debug`。官方长跑主要靠 `runstress`；runtime 单测多为短用例，靠 `go test -timeout`/dist timeout 兜底。行号对应 `/mnt/d/work/Compiler/go`；`run.sh`、`docs/STRESS_PLAN.md` 指本仓。目标：为 `stress/s01…s06` 提供模式，不改 `cases/` 契约。

## 1. 可复用模式

| 模式 | 依据（file:line） | 用法与注意 |
|---|---|---|
| 混合无限负载 + 子开关 | `go/test/stress/runstress.go:25-32,154-167`；`maps.go:104-113`；channel ring `:116-152`（100 goroutine、bufsize 0/1） | 每个 flag 启一个 forever goroutine，`select{}` 保活，`log.Fatalf` 立即退出。适合 s10 多租户；单场景必须补时长/停止/通过标记 |
| 资源闸门与限速 | `runstress.go:105-113`（exec 并发上限 10）、`:84-103`（校验 stdout+exit code）、`:52-53`（dial 后 250ms，防端口耗尽） | 所有 worker 用 token/信号量限并发；按 fd/端口/进程数限速，避免把环境耗尽误判成编译器 bug |
| 真实分配器压力 | `parsego.go:66-75`（反复 parse GOROOT 生成 AST 垃圾）；`maps.go:32-102`（map 增删/遍历/写入标记值） | 用真实解析/序列化产生多样对象；map 扩容、遍历、GC 交织；不要只分配等长 []byte |
| 持续 GC 负载 | `malloc_test.go:531-543`（独立 goroutine 无限 `runtime.GC()`）；`gc_test.go:517-540`（GOMAXPROCS-1 个 goroutine 反复 build tree）；`:583-602`（GC goroutine 保持写屏障常开） | GC 负载与业务 worker 分离；stop chan + WaitGroup 收尾；不要把 GC 次数/绝对堆量当断言 |
| 栈增长 + GC 交织 | `stack_test.go:150-182`（深递归每层写 `x=i+1`，返回途中校验）；`:80-143`（普通/locked/finalizer 上下文）；`:875-926`（P=4×N=10000×D=200 的 defer 堆/栈分配 + 强制 GC）；`chan_test.go:595-624`（阻塞 send 时 GC 缩栈） | 帧内放可扫描指针，`//go:noinline` 防优化；GC 放在增长/展开点；`debug.SetMaxStack` 防意外真溢出 |
| 抢占风暴 | `proc_test.go:319-372`（紧循环 + 并发 GC）；`testdata/testprog/preempt.go:17-63`（GOMAXPROCS=1、GC off、空死循环/frameless，强制异步抢占；实现说明见 `preempt.go:1-58`） | s04 直接复用；`asyncpreemptoff=1` 只作对照，不可与“空循环等 GC”同开（会 STW 等不到安全点） |
| 有界轮询/重试，不 sleep 判结果 | `gc_test.go:168-201`（最多 200×5ms 等 NumGC≥2）；`:218-235`（栈移动偶发被抢占干扰，重试 5 次）；`proc_test.go:401-431`（NumGoroutine 与全栈计数最多比 10 次） | 等“事件发生”而非固定时长；给 slack，失败才超时；一次采样不通过就判死是误报源 |
| 子进程隔离崩溃 | `crash_test.go:81-133`（build 一次、runTestProg 断言输出）、`:137-210`（build 并发限 2）；`:290-310`（stack overflow 输出前缀/子串）；`crash_unix_test.go:57-148`（SIGQUIT 全线程栈，数帧数） | 子进程崩溃不影响 harness；预期崩溃只断言输出/信号，不要求 exit 0；记录 exit status 与 signal |
| 自 re-exec / 入口分派 | `crash_test.go:37-49`（TestMain 按 env 进 panic/trap 入口）、`:973-1005`（`TEST_G0_STACK_OVERFLOW=1` 子进程）；`runtime/debug/stack_test.go:22-38` | 同一 binary 可作正常/崩溃/监控三种角色；只传必要 env，避免父进程配置污染 |
| core/crash 收集 | `crash_test.go:51-64`（TestMain 前后 stat `core`，发现新 core 即失败）；`crash_unix_test.go:94-105,168`（`cmd.Dir=t.TempDir()` + `GOTRACEBACK=crash`）；`runtime/debug/stack_test.go:134-187`、`example_monitor_test.go:50-97`（`SetCrashOutput` + monitor 子进程落盘 crash 文本） | core 受 `ulimit -c`/`core_pattern` 影响，不能保证；要可移植 crash 文本用 SetCrashOutput monitor；`GOTRACEBACK=crash` 至少慢 0.1s，超时要留余量 |
| 就绪握手 | `crash_unix_test.go:111-129`（ExtraFiles pipe，读 1 字节再发 SIGQUIT）；`:185-197`（`x\nx\n` 后仍 sleep 100ms，作者明确承认是竞态补偿） | 用 pipe/atomic/channel 握手；sleep 只能作为兜底，不能作为唯一同步或判定条件 |
| 不变量：内容/可达性 | `gc_test.go:42-124`（GC 后校验结构体/切片/链内容）；`:278-305`（可达性位图，允许 1 个保守残留 slop）；`map_test.go:277-305`（遍历中扩容+GC，最终 key 位图全齐）；`runtime.KeepAlive` | 断言“值未变/键未丢/活对象仍可达”；不比较普通地址；保守扫描的 slop 要显式允许 |
| finalizer 同步 | `mfinal_test.go:53-72`（GC 后 `<-ch`）、`:56-58,245`（tinyalloc 对象可能不触发 finalizer）；`stack_test.go:124-138`（`t.Deadline` + AfterFunc，未启动/未返回即 panic 打全栈）；`gc_test.go:884-904`（`checkfinalizers=1` 报 finalizer/cleanup 泄漏） | 不 sleep 等 finalizer；用带超时 channel；finalizer 要短且不能阻塞；对象须含指针且确实不可达 |
| goroutine 泄漏判定 | `goroutineleakprofile_test.go:33-47,507-543`（重复跑、区分 expected/flaky、零容忍误报）；`proc_test.go:401-431`（NumGoroutine 与栈计数重试对齐） | 运行时 GC/定时器/finalizer goroutine 会误判；先 quiesce 再多次取稳定值；优先 started/finished 计数与 profile diff |
| 超时/watchdog | `internal/testenv/exec.go:145-234`（按 t.Deadline 生成子进程超时、先 SIGQUIT、WaitDelay 宽限、Cleanup 检查已 Wait）；`testing/testing.go:2953-2968`（超时 panic 打全栈）；`stack_test.go:124-138`（AfterFunc + panic 保留栈） | 内层看心跳，外层看进程超时；SIGQUIT 拿全栈再 kill；WaitDelay 处理输出管道未关（`crash_test.go:124-127` 有 ErrWaitDelay 案例） |
| 差分/oracle | `run.sh:228-278`（ORACLE 构建 + 逐行 diff）；`docs/STRESS_PLAN.md:99-103` | 同 seed/profile 比最终校验和、计数、退出码；不比墙钟/内存绝对值。官方 runtime 不做双编译器差分，本仓 ORACLE 是增量 |

## 2. GODEBUG 开关用法

| 开关 | 语义/依据 | 压测用法 |
|---|---|---|
| `clobberfree=1` | 释放时把对象内容填坏（38-40） | UAF/读已释放必开；官方 `stack_test.go:869`、`crash_test.go:1082`；arena 测试在此模式下 SEGV 直接 skip（`arena_test.go:40-41`）；会改变“读 freed 内容”类用例的预期 |
| `efence=1` | 每对象独占页、地址不复用（94-96） | 强 UAF/悬挂指针；内存暴涨，只短跑/小负载 |
| `gcshrinkstackoff=1` | 禁止缩栈，栈只增（107-108） | s03 对照：区分增长/复制与缩栈两条路径；不适合长时间大栈 |
| `asyncpreemptoff=1` | 关异步抢占（`runtime/extern.go:231`）；官方用于稳定 SIGQUIT 栈（`crash_unix_test.go:101-104`）和泄漏 profile（`goroutineleakprofile_test.go:510-512`） | s04 控制组；不可与空死循环 + 等 GC 同用 |
| `gcstoptheworld=1/2` | 1=GC 全 STW；2=再关并发 sweep（110-112） | 与默认并发 GC 对比；dist 在 long 模式跑 archive/zip（`cmd/dist/test.go:806-824`）；会显著改变竞态暴露 |
| `gctrace=1` | 每次 GC 打一行 stderr（114-130） | 只做遥测；长跑日志量大，优先 `runtime/metrics` 周期采样 |
| `gccheckmark=1` / `checkfinalizers=1` | 二次 STW mark 验证（98-102）；finalizer/cleanup 泄漏检测（55-68） | 夜间小场景；checkfinalizers 适合 s02/s09，输出以 `checkfinalizers:` 开头；开销大，不进 60s CI |
| `GOGC=1`/`GOMEMLIMIT`/`GOMAXPROCS` | 本仓 profile：`run.sh:64-75` | soak=GOGC=1+clobberfree+GOMEMLIMIT+GOMAXPROCS=2；GOMEMLIMIT 必须高于真实 live heap，否则 GC 抖动；只作压力形状，不作断言 |

CoreGoLLVM 若未实现某 GODEBUG 开关，设置会被静默忽略；每条“开关下才复现”的结论都要有同场景/同 seed 的 base 对照。

## 3. 推荐 stress harness 骨架

- **进程结构**：runner 每场景/profile/seed 起一个子进程；env 白名单显式传入（不继承）；stdout/stderr 落 `build/stress/<scenario>.<profile>.<seed>.log`；检查 `STRESS PASS`，区分超时 124、信号、app FAIL。
- **阶段**：preflight（打印 runtime.Version/GOOS/GOARCH/GOGC/GODEBUG/GOMAXPROCS/seed/workers）→ warmup → steady（多 worker + GC churn）→ phase variants（shrink/regrow、GOMAXPROCS 1/2/4、cancel/restart，只在阶段边界改）→ quiesce（停入口、drain、停 timer、等计数）→ verify → metrics → pass marker。
- **心跳/watchdog**：官方没有业务心跳，只有测试超时；heartbeat 需自建。每 worker 每完成一个工作单元 `atomic.AddInt64(&beats[w],1)`，阶段号原子更新；watchdog 每 H 检查全局进度，3H 无增长则打印 `FAIL phase=... seed=...` + `pprof.Lookup("goroutine").WriteTo(os.Stderr,2)` 后 exit 3。内层只能抓“工作流卡死”；runtime 真死锁/STW 卡死靠 runner 外层 `timeout -s QUIT -k 30 $((STRESS_SECONDS+120))`。
- **不变量**：消息序号/校验和、环形保留对象内容、fresh 分配全零、无重复 ID、started==finished、map key 集完整、指针目标与值一致。所有阈值只允许“结构/相等”判定。
- **失败复现**：FAIL 行固定含 scenario/phase/worker/iter/seed/want/got，打印全 goroutine 栈；保留 binary、env、log；per-worker seed 由 `UAF_SEED` 混合 worker id 派生；支持 `STRESS_SECONDS=0` + 固定轮数重跑同 seed。
- **子进程/core**：子进程独立 cwd；默认 `GOTRACEBACK=crash`，退出后把 `core*` 移入 build；要求可移植 crash 文本时在程序内启动 SetCrashOutput monitor；60s CI 仅在崩溃时保留 core（受 ulimit/core_pattern 限制）。
- **metrics**：阶段边界读 `runtime/metrics` 快照（`/gc/cycles/total:gc-cycles`、`/sched/goroutines:goroutines`、`/memory/classes/heap/objects:bytes`、`/memory/classes/total:bytes`、`/gc/pauses:seconds` 直方图）+ 自增计数器（ops/checks/retries），只记录不判阈值。

## 4. 易踩坑

- **sleep 断言**：官方也有 1ms/10ms/100ms 的排序 sleep（`chan_test.go:620,773`、`crash_unix_test.go:185-197`），但都只用于“尽量错开”；判定必须靠 channel/pipe/计数。不要用 `time.Sleep` 后采样一次 memory/goroutine 数就判泄漏。
- **绝对内存阈值**：`TestGcSys` 因 16MB 阈值被标 known-flaky 直接 skip（`gc_test.go:30-40`）；`TestStackMem` 也用 8 倍估计和 4MB 粗界（`stack_test.go:64-76`）。压测层只记录 heap 峰值/斜率，不做硬阈值。
- **goroutine 泄漏误判**：`NumGoroutine` 含 runtime 自身 goroutine，退出中的 goroutine 也会瞬时计数；用 quiesce + 多次稳定采样（`proc_test.go:410-431`）或 started/finished 计数，pprof 只作诊断。
- **finalizer 时序**：finalizer 异步、单 goroutine、可能因 tinyalloc/栈残留不触发（`mfinal_test.go:56-58,245`）；等待必须带超时；负向断言（“不得提前跑”）官方也只有 `runtime.KeepAlive` + sleep（`mfinal_test.go:239-256`），压测层应改成“提前回收必导致内容损坏”的正向检测。
- **cgo/asm**：cgo/外部链接会关闭死锁检测（`crash_test.go:244-250`），cgo 测试对并发负载敏感（`crash_cgo_test.go:31-35`）；`-race` 会改变调度（`proc_test.go:443-446`），只作一次性预验证，不进 soak profile；`maymorestack` 是 Go 编译器测试钩子（`test/maymorestack.go`、`crash_unix_test.go:79-85`），不要假定 LLVM 后端支持。
- **env 污染**：monitor/子进程若继承 GOGC/GOMEMLIMIT/GODEBUG 会改变压力形状（`example_monitor_test.go:87-90`）；每个 child 显式给 env。
- **退出码/日志**：`timeout` 的 124 与程序自身 124 不可区分；`log.Fatalf` 不走 defer、不产 core；统一 fail helper 打上下文后再 exit 1。
- **调试开关副作用**：efence 会吃光内存、clobberfree 会让“读 freed”程序直接崩、gctrace 在小时级跑会产生 GB 级日志；这些是预期现象，必须和 base/control profile 对照后再判 bug。

## 5. 对 s01…s06 的具体建议

| 场景 | 复用模式 | 关键不变量 | 注意 |
|---|---|---|---|
| s01 service soak | runstress 的 httptest 服务（`runstress.go:57-82`）+ 自建 goroutine-per-request；有界并发；map 遍历中扩容 + GC（`map_test.go:277-305`） | 响应校验和、请求序号、缓存 key/value 完整 | 限制连接/请求并发与 body 大小；复用 `httptest` 端口而非反复 dial；`net.Pipe` 可绕开端口 |
| s02 heap cycle soak | 指针密集环形保留 → 丢一半 → `debug.FreeOSMemory` → regrow；GC churn goroutine | 存活对象 checksum、fresh 分配全零、无重复 ID | 不测“内存是否回到基线”；efence 只短跑；clobberfree 必开 |
| s03 stack churn soak | 深递归 + 帧内指针槽 + GC（`stack_test.go:150-182`）；panic/recover；anchor goroutine 跨 GC 持深栈 | 每层槽值、展开后 payload、panic 恢复后计数 | `//go:noinline`；`debug.SetMaxStack`（`runtime/debug/garbage.go:105-118`）限深；`gcshrinkstackoff=1` 作对照；避免超过 1GB 栈限制 |
| s04 preempt storm soak | `AsyncPreempt` 结构（`testdata/testprog/preempt.go:17-63`）+ `TestPreemptionGC`（`proc_test.go:346-372`） | 指针目标值、每 worker 完成计数、GC 前后内容 | GOMAXPROCS 只在阶段边界切 1/2/4；asyncpreemptoff 控制组不能配空死循环；`-race` 不混入 |
| s05 generated code soak | 大 struct/方法集/switch 表 + json/gob 往返；GC 放在 encode/decode 之间 | roundtrip 校验和、字段值、字节长度 | 保留编码缓冲跨 GC；clobberfree 下查 alias/复用；不在运行时生成代码 |
| s06 queue pipeline soak | 多级有界 channel + 背压 + cancel + timer；`TestSelectStackAdjust`（`chan_test.go:719-782`）的 channel 指针槽 + GC | 消息序号/顺序/无丢无重、started==finished、timer 已停 | 每阶段独立 heartbeat；shutdown 要 drain 并等 worker；不要用 NumGoroutine 一次采样判泄漏 |

落地顺序建议沿用 `docs/STRESS_PLAN.md:107-112`：s02/s03 试点先跑通 60s × 3 profile，再补 s01/s04/s05/s06；所有场景先在官方 Go 1.24.1 + `-race` 下验证自身无竞态，再纳入夜间。
