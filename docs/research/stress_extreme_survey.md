# 比现有 30 场景更极端、对编译器 UAF 更有挑战的压测形态调研

> 2026-11-26。范围：只新增本文，不改 `stress/`、`cases/`。依据：`docs/research/stress_scenarios_survey.md`、`stress/README.md`、`GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md`（下称 R）、`/mnt/d/work/Compiler/go`（Go 1.28-devel；下称 `rt/`=src/runtime、`tp/`=testdata/testprog、`tpc/`=testdata/testprogcgo）、公开 issue 与业界资料。
> 缩写：`gp/`=gollvm/passes；`go#N`=https://github.com/golang/go/issues/N；`R:A4`=报告 §2 风险点 A4。

**结论先行**：现有 30 个是**运行时语义 soak**，不是**编译器 IR 形状压测**。CoreGoLLVM 的 UAF 多由特定 IR 形状（聚合按值、局部指针数组、RVO/defer、汇编帧、inline/尾调用）触发；30 个场景很少生成这些形状，GC 注入点又粗，探针只覆盖“对象已释放/复用”这一种失败。所以“没测出来”首先是形态与判定问题，不是时长问题。

## 1. 归因：现有 30 个为什么没复现

| 归因 | 证据 | 后果/改进方向 |
|---|---|---|
| **路径未覆盖**（首要） | R 的 22 个风险点至少 11 个没有针对性场景：A4 deopt（聚合参数）、A5 FCA、A7 pcdata 边界、A8 args/locals、A9 stackObjectRecord、A12 inline、B14 全局 gcprog、B15 gcprog 缓存、C17 typedmemmove、D19 RVO、D20 tailcall。`grep stress/*/main.go`：无 `weak.`、`AddCleanup`、`LockOSThread`、`iter.Pull`（coro）、`synctest`、`SetPanicOnFault`、`Pinner`、`//go:linkname`、`.s`。A10 morestack 只被 s03/u09 打、A11 defer 只被 u08 打、A13 抢占只被 s04/u02 打，全是二元组合 | 需要 x01/x03/x05/x06/x09/x10/x11/x12/x13 的新 IR 形状 |
| **压力不足** | 默认 `SUITE=stress` 只跑 `soak`（GOGC=1+GOMEMLIMIT=256MiB+GOMAXPROCS=2+clobberfree）；无 efence/gccheckmark/gcstoptheworld/checkfinalizers。除 u03 外 GOMEMLIMIT 远高于 live heap，assist 很少被逼出来；仅 s03/u07/u08/u09 用 `SetMaxStack`；GC 注入在阶段/轮边界，不在每次 call；cgo 仅 n07，且无 panic/深栈/GOMEMLIMIT | 新增 window/detector/finale 三类 profile；x07/x08/x13 |
| **窗口未收窄** | GC 注入是“每轮/每阶段”粗粒度，不是“最后使用→GC→再次使用”的精确点；没有按 call 穷举注入；没有 fault schedule/step replay；GOMAXPROCS 只在阶段边界切；没有外部 SIGURG 洪水；没有“指定调用处必触发 morestack” | x13；§3.2/3.3 |
| **探针不敏感** | canary 只能证明“对象已被释放/复用”；若漏标对象仍被其它根保活，或直到场景结束才被释放，探针永远不会失败。无 `weak.Pointer`“持强引用却被回收”的直接判据；finalizer 只测“可达不终结”，无复活/AddCleanup 顺序；无 gccheckmark 二次标记校验；小对象在 efence 下不 fault（§3.2 实测）；探针可能被优化/提升；无 stock vs CoreGoLLVM 差分 | x14 + §3.4 探针升级 |

两个结构性原因：
1. 30 个先在 stock Go 1.24.1 通过，只覆盖 stock 也保证的语义；新近/未修复的编译器或运行时问题（go#82026、go#82394、go#82917、go#80188）正好不在其中。R §4 的 P0 1–9 中，#2（命名返回+defer）、#4（条件分支内大指针数组）、#5/#6（FCA/byval）、#8（全局大对象）在 stress 层仍无对应。
2. “偶现”是概率问题：go#80147 复现需数小时到数天；go#38931 自述约 2% 命中（`tp/lockosthread.go:211-213`）。窗口只有几条指令时，随机长跑命中率极低，必须把窗口变成必然（§3）。

## 2. 新场景清单（x01–x15）

业界可迁移点（先给依据，再给场景）：

| 来源 | 关键做法 | 迁移 |
|---|---|---|
| [jcstress](https://github.com/openjdk/jcstress) | 多 actor/arbiter、多 state 实例、outcome 分类、每轮 deopt、spin/yield/park 多策略、概率统计 | 学探针/outcome 设计；它本身也不保证复现，故更需 x13 |
| [TigerBeetle VOPR](https://github.com/tigerbeetle/tigerbeetle/blob/main/docs/internals/vopr.md) | stub clock/network/disk、每 IO 注错、seed+commit 精确重放、时间加速 | x13 的 fault schedule 与 seed+step 日志 |
| [FoundationDB Simulation](https://apple.github.io/foundationdb/testing.html) | 单进程确定性模拟、故障注入、seed 回放 | Go 无 runtime stub，退化为“操作脚本+穷举注入点” |
| Go issue tracker | 局部指针数组跨 GC call（#82026）、panic/Goexit 帧位置（#82394/#82917）、async preempt 小帧（#80188）、大栈陈旧值+SIGURG（#80147）、GOMAXPROCS 增长+idle M 的 allpSnapshot UAF（#74414）、汇编坏 FP+pprof（#74851）；另有 oldm/STW（`tp/stw_mexit.go`）、并发 type metadata（`tp/typelinksrace.go`） | x02/x05/x13/x04/x09/x11 |
| Fission/Weave/Avalonia | 经 GitHub/Crossref 检索**未找到**对应的 Go 并发压测工具（Fission=K8s serverless、Weave=容器网络、Avalonia=.NET UI）；若指内部工具需补链接 | 以 Go 官方 `test/stress/runstress.go`、[x/tools/cmd/stress](https://pkg.go.dev/golang.org/x/tools/cmd/stress) 替代 |

| ID/名称 | 形态 | 来源 | 三重组合 | 为什么比现有更狠 | stdlib；代码量；优先级 |
|---|---|---|---|---|---|
| **x01 aggregate_statepoint** | noinline 函数返回/传入 `[2]*T`、`[2][2]*T`、`struct{A [2]*T; B [2][2]*T}`、`[1]struct{P *T}`；int/string/slice/iface 混合；byval>128B；goret+byval+普通参数混排；callee 内 GC 后解引用；8 种泛型实例化 | R:A4/A5/A8；`gp/GoExtractFCAForGC.cpp:236-279`（261/277 `llvm_unreachable`）；`gp/GoFuncInfoHandler.cpp:101-190` | FCA × byval/args-locals 边界 × call/return GC | u01 只做堆对象字段；n04 无 byval；聚合抽取不支持嵌套数组，release 下静默 miscompile | 是；600–750；P0 |
| **x02 localarray_liveness** | `arr := [2]T{...}`（T 含 `*payload`）；非常量下标；call 前取 `&arr[m].p`，call 内 GC，返回后读 `arr[m]`；noinline；GOGC=1 | go#82026；R:zero point 3420-3454 | CSE/liveness × 局部指针数组 × GC-in-call | 现有无“取址先于 call、下标非常量”的形态；go 1.24–1.28 仍可复现的编译器 bug | 是；450–550；P0 |
| **x03 namedret_defer_rvo** | `func f() (r *Big)` + defer 改 r、大 struct 按值返回、deferreturn 处 GC、panic/recover；多返回路径 | R:D19/P0#2；`gp/GoReturnValue.cpp`；`gp/GoProcessDefer.cpp`；`rt/stack_test.go:244-328,868-929` | named-return/RVO × defer/deferreturn × return 处 GC | u08 只打 panic 展开，无命名返回/RVO/deferreturn PC | 是；500–600；P1 |
| **x04 growstack_thread_stw** | GOMAXPROCS=4；反复 `LockOSThread` 后 goroutine 退出（oldm）；`ReadMemStats` 制造 STW；深递归+finalizer 内 growStack；SetMaxStack 边界；GC 风暴 | `rt/stack_test.go:80-183`；`rt/proc_test.go:1010-1043,1580`；`tp/lockosthread.go:205-250`；`tp/stw_mexit.go:1-69` | morestack/shrink × LockOSThread/线程退出+STW × GC/finalizer | s03/u09 只做栈增长/收缩；无 LockOSThread/线程退出/STW/oldm | 是；500–650；P1 |
| **x05 goexit_panicframe** | 大 struct 按值传参、闭包取址形成 dead stack object；drop 强引用→GC→Goexit；defer 内再 GC；嵌套 panic+`Callers` 栈走查；panic+defer+WaitGroup+Gosched 变体（`tp/panicrace.go:1-27`） | go#82394；go#82917；`tp/gcpanicframe.go:32-64`；`rt/crash_test.go:427-439`；`rt/stack_test.go:349-427` | `_panic.fp/sp` 帧位置 × dead stack object × defer/栈走查中 GC | u08 校验展开帧内容，不测“panic 记录保活死对象”；2026 新 bug | 是；450–550；P0 |
| **x06 coro_iterpull** | `iter.Pull` 生成的 coro 百万次/秒切换；pull 结构含指针/interface/panicValue 跨 coroswitch；GC 风暴；LockOSThread+cgo 回调变体；stop/嵌套 Pull | `rt/coro.go:1-260`；`rt/coro_test.go:15-77`；`src/iter/iter.go:233-237,261-350`；`tpc/coro.go:1-185` | coro（m 栈切换）× GC 扫两个 goroutine 栈 × LockOSThread/cgo | 30 个场景完全无 coro；`//go:linknamestd` 是新指令，CoreGoLLVM 很可能未适配 | 是（cgo 变体需 cgo）；550–700；P0 |
| **x07 cgo_panic_deepstack** | C 线程回调→Go 万层递归（栈增长）→panic/recover→回 C→再回调；回调内 GC；GOMEMLIMIT 贴 live heap 强制 assist；Goexit 变体 | `tpc/callback.go:1-140`；`rt/crash_cgo_test.go:62-87,722-748,1037` | cgo(extra M) × 深栈/panic 跨 C 边界 × GC assist/GOMEMLIMIT | n07 只有回调递归+第 64 次 GC，无 panic/Goexit/栈增长/GOMEMLIMIT | 需 cgo；500–650+C；P0 |
| **x08 signal_altstack** | C 线程自建 sigaltstack 后回调 Go；Go 内 SIGSEGV→gsignal→sigpanic 穿过 C 帧；外部线程 SIGPROF；StackSwitchCallback（换 C 栈）；SetPanicOnFault | `rt/signal_unix.go:436-508,565-620,1337-1450`；`rt/crash_cgo_test.go:665,681,1013`；`rt/crash_unix_test.go:150-240`；`tpc/sigstack.go,stackswitch.go,sigpanic.go`；`rt/debug/garbage.go:139-165` | 信号 altstack × sigpanic 展开 × cgo/extra M | n06 只做 exec/SIGQUIT；n07 无 altstack；这是 gsignal `stktopsp` 专属路径 | 需 cgo；450–600+C；P1 |
| **x09 asm_abi0_stackmap** | `main.go`+`.s`：ABI0 函数带指针参数/返回值；asm 内 `CALL ·goHelper`，helper 分配+GC；asm 递归回调 Go；asm 破坏 BP 后 `Stack`/pprof 走查 | R:A8；R §3.2 P0#19；`GoAsmFuncStackMap`（R §1.2）；`tp/framepointer.go:1-90`；go#74851 | asm 帧 × args stackmap × Go callee 内 GC | 30 个 stress 无 `.s`；args stackmap 只有 littest 覆盖 | 是（+asm）；350+200；P0 |
| **x10 inline_tailcall_pcdata** | wrapper/method expression/interface 调用触发 musttail；末尾 call 处 GC；inline 树内指针跨调用；deferreturn 边界；`//go:noinline` 对照 | R:A7/A12/D20；`gp/GoFuncInfoHandler.cpp:690-791,806-838`；`gp/GoTailcall.cpp`；`gp/GoFixTraceback.cpp:56-115`；`rt/stack_test.go:657-796` | inline/musttail × pcdata/stackmap 区间边界 × last-call GC | 无场景针对“最后一条 call/尾调用/deferreturn”的 pcdata 边界 | 是；500–650；P0 |
| **x11 global_gcprog** | 巨型全局：`[1<<16]*T`、`[1<<14]Kv{k string; v *T; s []byte; e any}`、`chan [1<<10]Kv`、`map[string][1<<6]*T`；生成 64 种不同大类型；并发 `reflect.PointerTo` 打 typelinks；反复改全局+GC | R:B14/B15；`gollvm/gofrontend/go/types.cc:3432-3443`；`gp/Util.cpp:400-430`；`rt/gc_test.go:147-166`；`tp/typelinksrace.go:1-37` | 全局 gcprog/ptrdata × 大类型元数据/typelinks × 全局 scan/mark | s05 是 64 个小类型；无全局巨型对象；issue1002 的 gcprog 路径无人打 | 是+生成器；450+gen；P1 |
| **x12 typedmemmove_stackwb** | 含指针 struct 的 `copy/append/map assign/chan send/iface 装箱`；dst 在栈上（addr-taken）；old→young；批量 copy；bitcast/GEP 别名；GC 后校验 | R:C16/C17；`gp/GoTypedMemmove.cpp:47-66`；`gp/GoRemoveStackWriteBarriers.cpp:50-110`；`gp/GoWriteBarrier.cpp:39-83`；`rt/gc_test.go:623-718` | typedmemmove × 栈 dst/WB 删除 × 并发 GC | n04 是 `[]*T` 批量 copy；u01 是字段写；未打 typedmemmove+栈 dst | 是；550–700；P1 |
| **x13 safepoint_matrix** | 操作脚本（每步一个 closure）；对每个 call/lifetime 边界穷举注入 `runtime.GC()`；`gcstoptheworld=2`+`gccheckmark=1`+`clobberfree=1`+GOGC=off；GOMAXPROCS=1 串行+2 对照；失败打印 (shape,step,seed) | go#80188；go#80147；`rt/extern.go:94-113,231-240`；`rt/preempt.go:421-433`；`rt/signal_unix.go:342-367`；jcstress/VOPR | 任意 shape × 每个 call 边界 GC × STW 二次标记校验 | 现有全是随机/阶段 GC；无穷举注入与 gccheckmark | 是；650–850；P0 |
| **x14 finalizer_weak_resurrect** | 终结器把对象存回全局（复活）+再注册；AddCleanup 多注册/Stop/交错；`weak.Make` 断言“持强引用时 Value 非 nil、drop 后 nil”；interior/tiny/zero；checkfinalizers=1 | `rt/mfinal_test.go:24-115,218-257`；`rt/mcleanup_test.go:18-410`；`tp/checkfinalizers.go:1-102`；`src/weak/pointer.go:1-80`；`rt/extern.go:55-68` | 复活/清理顺序 × mark termination × weak 探针 | n03 无复活/AddCleanup/weak；“活着被回收”探针全缺 | 是；450–550；P1 |
| **x15 synctest_bubble** | bubble 内大量分配+finalizer/cleanup+定时器；确认 GC worker/finalizer goroutine 不加入 bubble；`Wait`/时间推进；GC 跨 bubble 状态变更 | `rt/synctest.go:1-100`；`src/testing/synctest/synctest.go:287-340`；`rt/testdata/testsynctest/main.go:1-92` | fake clock/durable blocking × goroutine 状态 × GC/定时器 | 无场景用 synctest；新运行时原语 | 需 `go test` 或 linkname internal；350–450；P2 |

**落地顺序**：P0 = x01 → x02 → x05 → x13 → x06 → x09 → x10 → x07；P1 = x03/x04/x08/x11/x12/x14；P2 = x15。表中每行都是三重组合；现有 30 中 A10/A11/A13 都只是二元（栈增长、panic、抢占各自独立）。
**不建议**：再堆 timer/netpoll/map/fuzz/LSM/MVCC/TLS（n01–n12 已密）；`mknacl` 已从 Go 移除；`msan/asan` 需 clang 并重建 runtime，本仓 `llvm-goc` 链路未验证；`arena` 需 GOEXPERIMENT，编译器支持存疑，暂列淘汰。

## 3. 确定性/窗口收窄策略

### 3.1 能力边界（先承认限制）
Go 没有 .NET `GCStress`/jcstress 级别的公开钩子。`runtime.GCTestMoveStackOnNextCall`、`GCTestIsReachable`、`GCTestPointerClass`、`SetSpinInGCMarkDone` 只在 `export_test.go`（测试二进制）存在（定义见 `rt/export_test.go:1436-1440,1984`，用例见 `rt/gc_test.go:218-332,805-903`），`package main` 不可用。因此可分三级：
- **A 级（确定性判据，命中即失败）**：efence（大对象/栈）/ gccheckmark / checkfinalizers；
- **B 级（穷举注入点）**：x13 操作脚本；
- **C 级（概率放大）**：GC storm、外部 SIGURG、长跑。现有 30 全在 C 级。

### 3.2 GODEBUG 组合矩阵

| 组合 | env | 作用 | 用法 |
|---|---|---|---|
| daily | `GOGC=1,GOMAXPROCS=2,GOMEMLIMIT=256MiB,GODEBUG=clobberfree=1,asyncpreemptoff=0` | 现有 soak | 回归 |
| window | `GOGC=off,GOMAXPROCS=1,GODEBUG=gcstoptheworld=2,gccheckmark=1,clobberfree=1` | 每次 GC 全 STW+同步 sweep，排除后台并发标记/清扫与窗口重叠；二次标记发现漏标即 panic | x13 短跑 10–30s |
| detector | `GOMAXPROCS=1,GODEBUG=efence=1` | 大对象/栈 free 即 sysFault（mgcsweep.go:850、stack.go:477-479） | 固定轮 1–3、对象少；OOM 快 |
| finale | `GODEBUG=checkfinalizers=1` | 可达 finalizer/cleanup 泄漏直接 throw | x14 nightly |
| layout | `GODEBUG=gcshrinkstackoff=1` | 固定栈布局便于二分；会掩盖 shrink 类 bug | 仅对照 |
| trace | `GODEBUG=allocfreetrace=1`（单对象定位）；`gcpacertrace=1`/`gctrace=1` 只看 pacer/阶段，不作断言 | 每次 alloc/free 打栈；pacer 观测 | 单对象短跑定位“谁 free 的”；调 assist 参数 |
| control | `GODEBUG=asyncpreemptoff=1` | 关闭异步抢占/保守栈扫描 | 对照；需要保守扫描的场景不得默认开 |

**实测提醒（Go 1.28-devel/linux-amd64）**：1MiB 对象在 `efence=1` 下 UAF 立即 SIGSEGV；32B 小对象在 efence 下同一 stale read 仍返回原值、未触发 fault（同结构程序换 1MiB 对象即崩溃）。`efence` 的 page-per-object 语义不能当小对象的通用判据，小对象必须配 weak 探针。另：`runtime.GC()` 会帮 sweep 做完再返回（mgc.go:560-566），所以显式 GC 后 `clobberfree` 是及时的；但 GOGC 触发的后台 GC 不保证，且前提是对象确实被释放——若漏标对象被其它根保活，任何 clobber/canary 都不会失败，必须靠 gccheckmark/weak。

### 3.3 故障注入与窗口收窄
0. **为什么偶现压不倒**：现有 30 的每次 GC 只是一个时间点，而 UAF 需要 GC 恰好落在“指针已死/尚未死”的 IR 边界上；重复 N 次只提高概率，不改变窗口宽度。x13 把注入点变成有限可枚举集合，覆盖率可度量、失败可回放。
1. **x13 操作脚本**：每个操作有 id，每个“可能成为 GC 点”的边界调 `maybeGC(opID, point)`；先跑所有单点注入，再跑有界双点；`GOMAXPROCS=1`+channel barrier 串行化；失败行打印 `(shape, i, j, seed, gcCount)`，可精确重放。这是把“偶现”变成“对每个 call 边界必测一次”的核心。
2. **寄存器窗口**：另一个 goroutine 触发 GC storm，同时向自身进程发 `SIGURG`（`syscall.Kill(os.Getpid(), syscall.SIGURG)`）。注意 `doSigPreempt` 仍被 `wantAsyncPreempt` 门控（`rt/preempt.go:421-433`），只有 G/P 已被 sysmon/GC 标记 preempt 才注入；所以 SIGURG 洪水是概率放大器，不是必然注入（go#80147 的复现方式）。
3. **栈移动窗口**：`debug.SetMaxStack` + 近上限递归，让指定 call 必然 morestack；返回后立即校验。无法在指令级注入，但可让每次 call 都成为增长点。
4. **可控时钟**：只有 `testing/synctest` 能伪造 timer/netpoll 时间；GC 没有 fake clock。FDB 式全确定性需要 runtime stub，stdlib 不可行，本文明确不承诺。
5. **心跳/超时**：沿用现有 watchdog；x13 额外记录“已覆盖注入点数/总点数”，未覆盖完不得报 PASS。

### 3.4 探针灵敏度升级
- `weak.Make(obj)` + anchor 强引用：**持强引用时 `Value()!=nil`** 是“未被提前回收”的直接证据，且不依赖内存复用，大小对象都有效；drop 后 `Value()==nil` 用于查漏回收。
- `AddCleanup` + channel：断言 cleanup 未提前运行、顺序正确、`Stop` 生效；终结器复活（存回全局）后检查弱指针语义与不重复终结。
- `gccheckmark=1`：二次标记发现“可达却未标”即 panic，独立于 clobber/reuse，是漏标的最佳判据。
- canary 三阶段：分配后/GC 前/GC 后；校验函数 `//go:noinline` + `runtime.KeepAlive`；GC 后按同 size class 批量分配迫使复用。
- `-gcflags=all=-d=checkptr=2`（stock 对照）、`-race`（只查数据竞争，不查 stackmap UAF）；两者不能替代上述。
- **差分 oracle**：同一源码 stock Go vs CoreGoLLVM，比较输出/校验和；runner 目前只对 unit 做 ORACLE，应扩到 stress。再加 `-O0` 与默认优化、`-fgo-abi=gc` 与 `gollvm`（若仍支持）、x86_64 与 aarch64 四组矩阵。

### 3.5 runner/build 建议
新增 profile：`window`（§3.2）、`detector`（短跑）、`finale`（nightly）、`layout`（bisect）；保留 `soak`。每场景用 `.skip-envs` 跳过不适用的重 profile。失败产物必须含：profile、GODEBUG、seed、shape、注入点 i/j、GC 次数、crash 文本（现有 `STRESS_CRASH_FILE` 已够）。

## 4. 与现有 30 个的差异表（避免重复）

| 新场景 | 最近现有 | 现有覆盖 | 新增、不重复的点 |
|---|---|---|---|
| x01 | u01/n04 | 堆对象字段/图的写屏障 | 聚合按值、byval、args/locals 边界、FCA 嵌套数组 |
| x02 | u02/s04 | 寄存器/接口持针、抢占窗口 | 局部指针数组 + 非常量下标 + 取址跨 call 的 liveness/zero point |
| x03 | u08 | panic 展开帧校验 | 命名返回/RVO/deferreturn PC/多返回路径 |
| x04 | s03/u09 | 栈增长与收缩 | LockOSThread、线程退出 oldm、STW、finalizer 内 growStack |
| x05 | u08/n03 | panic 展开、终结器生命周期 | Goexit/panic 记录保活 dead stack object（#82394/#82917） |
| x06 | 无 | 无 coro | coro m 栈切换、iter.Pull、跨 coroswitch 指针、cgo 回调 |
| x07 | n07 | C→Go 回调 + 递归 + 计数 GC | panic/Goexit 跨 cgo、栈增长、GOMEMLIMIT assist |
| x08 | n06/n07 | exec/SIGQUIT、cgo 信号 | gsignal/altstack 上的 sigpanic、换 C 栈、外部 SIGPROF |
| x09 | L08 cases | 汇编语义回归 | stress 级 asm args stackmap + Go callee 内 GC + FP 破坏 |
| x10 | s05/n08 | 生成类型/解析 | inline 树、musttail wrapper、last-call/deferreturn pcdata |
| x11 | s05/n08 | 64 个小类型 | 全局巨型类型、gcprog/ptrdata、gcprog 缓存 |
| x12 | n04/u01 | 手工字段写/`[]*T` 批量 copy | typedmemmove、栈 dst、WB 删除、bitcast/GEP 别名 |
| x13 | 全部 | 随机/阶段 GC | 穷举注入点 + gcstoptheworld=2 + gccheckmark 判定 |
| x14 | n03 | SetFinalizer 生命周期 | 复活、AddCleanup 顺序/Stop、weak 探针、checkfinalizers |
| x15 | 无 | 无 synctest | bubble/fake clock/durable blocking 与 GC worker 隔离 |

**重复度结论**：x01/x02/x06/x09/x10/x13 与现有场景基本正交；x03/x04/x05/x14 共享 panic/栈/终结器领域但触发路径不同；x07/x08 只与 n07/n06 共享“cgo/信号”外壳，内部路径不同；x11/x12 只与 s05/n04 共享“大对象/写屏障”背景，触发 pass 不同。

## 5. 关键来源
- Go 源码：`rt/coro.go:1-260`、`rt/synctest.go:1-100`、`rt/stack_test.go:80-183,244-427,868-929`、`rt/proc_test.go:1010-1043,1580`、`rt/gc_test.go:147-166,218-332,805-903`、`rt/signal_unix.go:436-508,1337-1450`、`tp/gcpanicframe.go:32-64`、`tp/stw_mexit.go:1-69`、`tpc/coro.go:1-185`、`tpc/callback.go:1-140`、`tpc/sigstack.go`、`src/weak/pointer.go:1-80`、`src/iter/iter.go:233-237,261-350`。
- 公开 issue：go#82026（局部指针数组 CSE/liveness）、go#82394、go#82917（panic 帧位置）、go#80188（async preempt 小帧）、go#80147（保守扫描+SIGURG）、go#74414（allpSnapshot UAF）、go#74851（汇编坏 FP+pprof）。
- 业界：jcstress（<https://github.com/openjdk/jcstress>）、TigerBeetle VOPR（<https://github.com/tigerbeetle/tigerbeetle/blob/main/docs/internals/vopr.md>）、FoundationDB Simulation（<https://apple.github.io/foundationdb/testing.html>）、Go `test/stress/runstress.go`、`x/tools/cmd/stress`、.NET GC/JIT stress（<https://github.com/dotnet/runtime/blob/main/docs/design/coreclr/jit/investigate-stress.md>）。
- 核实否定：Fission/Weave/Avalonia 未能检索到与 Go 并发压测对应的工具；`mknacl` 不存在；`msan/asan` 需 clang 并重建 runtime，本仓 `llvm-goc` 链路未验证，不作为场景前提。
