# stress —— 长跑压测层

与 `cases/` 的 19 维语义用例并列的**长跑层**，不占 19 维配额。当前 **67 个场景**（s/n/u/x/g/y + z01–z09 生命周期类），全部通过官方 Go 1.24.1 固定轮数 + soak 验证；s01–s06 另有 1h 基线。**u 系列专打 UAF**：canary magic + finalizer 探针 + clobberfree，让任何漏标当场现形。

## 约定

- 每个场景一个目录：`stress/<name>/main.go` + `scenario.md`（含不变量/参数/失败判读/来源）。
- 仅标准库（n07 需 cgo，标 `requires: [cgo]`）；单包 `package main`。
- 统一标记：成功最后一行 `STRESS PASS <name>`；失败 `FAIL:` 并退出 1。
- 参数（环境变量）：

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0` 表示固定轮数（快速自检） |
| `STRESS_CRASH_FILE` | runner 注入 | crash 文本落盘路径（`debug.SetCrashOutput`），core 不可用时用它 |
| `STRESS_WORKERS` | 场景默认 | 并发 worker/租户数 |
| `STRESS_HEARTBEAT` | 5 | 心跳检查间隔（秒），停滞 6 个周期判失败 |
| `UAF_SEED` | 1 | 随机路径种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 场景默认 | seconds=0 时的轮数/消息数 |
| `STRESS_GMX`（n01/s04） | 空 | 阶段边界切换 GOMAXPROCS，如 `1,2,4` |

x 系列 profile（穷举/判定强化，见 `docs/research/stress_extreme_survey.md` §3）：

| profile | 环境 | 用途 |
|---|---|---|
| `window` | `GOGC=off GOMAXPROCS=1 GODEBUG=gcstoptheworld=2,gccheckmark=1,clobberfree=1` | 全 STW + 二次标记，漏标即 panic（不依赖对象复用） |
| `detector` | `GOMAXPROCS=1 GODEBUG=efence=1` | 大对象 free 即 fault；短跑/固定轮 |
| `finale` | `GODEBUG=checkfinalizers=1,clobberfree=1` | finalizer/cleanup 泄漏即 throw（x14） |
| `layout` | `GODEBUG=gcshrinkstackoff=1,clobberfree=1` | 固定栈布局，二分定位用 |

- 场景内部要求：阶段化、持续不变量、心跳/watchdog、指标只记录不断言、结束 quiesce + goroutine 基线。

## 场景目录（67 个）

### s01_service_soak — 进程内服务（P1，L02/05/06/17/18/19）
httptest HTTP/JSON：多客户端 worker 持续请求（id/name/vals），服务端解码、求和、写 map+ring+interface 缓存（容量淘汰），阶段含 shrink（丢缓存 + FreeOSMemory）+ regrow。
**测**：JSON 编解码分配、缓存指针跨 GC、goroutine-per-request 生命周期。
**不变量**：响应 id/sum/name 正确、缓存内容完整、收尾 goroutine 回基线。

### s02_heap_cycle_soak — 堆循环（P1，L05/13/18）
指针密集 slab（`[]*node`+`[]kv`+interface+链）环形保留 3 份 → 丢弃/FreeOSMemory → 再增长；独立 GC churn。
**测**：分配零化、FreeOSMemory 收缩/再增长、存活对象内容。
**不变量**：fresh 分配全零、ring 内 slab 抽样内容/sum 不变。

### s03_stack_churn_soak — 栈增长/展开（P1，L04/17）
1500 层递归（每层 32 指针槽）逐层展开校验、GC 穿插增长/展开点；panic/recover（512 层 payload）、递归闭包、64KB 大帧、栈收缩后再进入。
**测**：morestack 栈复制时指针重定位、展开路径、panic payload 存活。
**不变量**：每层槽值与入参关系、递归 checksum = 公式。

### s04_preempt_storm_soak — 抢占风暴（P0，L03/02）
无函数调用紧循环（slice 轮转、持指针、unsafe 单表达式往返）+ 每 N 轮 noinline GC + 独立 GC churn；可选 `STRESS_GMX` 切 GOMAXPROCS。
**测**：异步抢占落在长循环内时的栈扫描、寄存器指针。
**不变量**：紧循环闭式校验和、cell 指针内容。

### s05_generated_code_soak — 生成代码（P1，L10/06/11/17）
`tools/gen_stress_code.py` 生成 9590 行（64 record 类型、1024 分支 dispatch、getter/setter、深拷贝、描述表）；多 worker 持续 JSON marshal/unmarshal、dispatch、深拷贝、接口调用 + GC。
**测**：大 switch/方法集/深拷贝在真实生成形态下的正确性。
**不变量**：roundtrip 校验和、深拷贝独立性、指针字段内容。

### s06_queue_pipeline_soak — 流水线（P1，L02/13/19/17）
producers → 有界 in → 2×stage1（校验/转换 + map 缓存）→ mid → 2×stage2（interface 装箱/深拷贝）→ out → aggregator；优雅关闭逐级 drain。
**测**：channel 传输指针、背压、取消传播、优雅关闭。
**不变量**：每 producer 序号连续不丢不重、checksum、started==finished。

### n01_timer_netpoll_storm — timer/netpoll 风暴（P0，L19/04/02）
每 worker：多 Timer 收取→Stop→Reset 风暴（统计 arm 结局）；AfterFunc 自重挂链；ticker 双消费者竞争；context 超时树 + WithDeadline；loopback TCP accept/echo/半关/已过期 SetReadDeadline。
**测**：timer 堆重挂、netpoll 唤醒、fd 生命周期、调度饥饿。
**不变量**：回调计数=重挂数、同 tick 不重复投递、echo 字节、Done/Err 语义。

### n02_map_table_churn — map 翻腾（P0，L13/18/06）
分片 map（int64/struct key）owner 写 + 读锁并发抽查；遍历中删除、重插、周期 clear；sync.Map 并发 Load/Store/Range/Delete；持锁 reflect.MapKeys 抽查并把指针带出锁外 GC 后复核；NaN key 边界。
**测**：map 扩容搬移、迭代器旧 bucket 生命周期、写屏障。
**不变量**：key 集与模型一致、值内容、started==finished。

### n03_gc_lifecycle_finalizer — finalizer/weak（P0，L18/05/13）
分配→丢弃→GC 循环：普通对象（anchor 可达不终结）、interior pointer 字段（`&base.pad[1]`）、tiny/zero-size、Pool+finalizer；collector 位图查重、live set 查“可达被终结”；收尾 noinline 失活探针 + 超时。
**测**：finalizer 触发/丢失/重复、对象复活、内部指针 pin。
**不变量**：可达不终结、失活最终终结、payload 内容。

### n04_write_barrier_graph — 写屏障图（P0，L18/05/17）
先晋升老年代：4095 节点二叉树、1024 链、map 256 项、`[]*gnode` 4096 项、批量 copy src/dst。每轮：old→young 指针/interface/map 写、子树交换、批量 copy/append、删+重插；每 2 轮 GC 验证 young 可达，每 8 轮全图校验。
**测**：写屏障在 old→young/bulk copy/interface 的完整性（漏标→对象被回收）。
**不变量**：全图 checksum、young 链 GC 后可达。

### n05_trace_pprof_metrics — 可观测性（P1，L04/05/14）
worker 在 `pprof.Do` label 内做深递归/chan/分配；观察者反复 trace Start/Stop、四类 profile WriteTo、metrics 周期读。
**测**：栈回溯正确性、SIGPROF/trace 死锁、profile label race。
**不变量**：trace 头标记、profile 文本头、metrics 键存在且单调。

### n06_exec_signal_lifecycle — exec/信号（P1，L02/04/19）
自 re-exec 四种子模式（echo/hang+cancel/panic/SIGQUIT）；并发 fork/exec（上限 10）+ pipe 校验输出；子进程剔除 STRESS_CRASH_FILE 防覆盖。
**测**：fork/exec fd 继承、pipe 生命周期、SIGQUIT 转储路径。
**不变量**：exit code/输出前缀、僵尸回收、goroutine 基线。

### n07_cgo_storm — cgo 回调（P1，L05/09/17，需 cgo）
C 侧 pthread 线程回调 `//export goCb`（每次分配+校验、1/23 深递归 24 层、全局第 64 次 GC）；Go 调 C 阻塞 nanosleep；`NumCgoCall` 只记录。
**测**：cgo 切换时栈/寄存器可扫描、extra M、信号转发。
**不变量**：回调校验和与纯函数期望一致、计数一致。

### n08_parser_typecheck — 解析/类型检查（P1，L01/05/16）
运行时生成 ~2000 行合法 Go（泛型/接口/闭包/defer/type switch）→ ParseFile（0 错误）→ GC → types.Check（全量 Info）→ printer 格式化；AST/typeinfo 快照跨 GC 复核。
**测**：前端深递归、AST 对象图存活、FileSet/map 生命周期。
**不变量**：AST checksum、类型数、格式化输出逐轮一致。

### n09_lsm_storage — mini-LSM（P2，L02/05/13）
内存 WAL/memtable/不可变队列/L0/L1 SSTable/compaction（墓碑清理）/快照迭代器；随机 op 并发写删读扫 compact；value 带 crc32。
**测**：buffer 复用 UAF、旧版本迭代、compact 与 flush 并发。
**不变量**：快照 digest 跨写入/compaction 不变、全量 Get 与 model 一致、WAL seq 单调。

### n10_mvcc_watch_lease — MVCC/watch（P2，L02/05/13/18）
revisioned KV + watcher（prefix/range/start-rev 回放，compaction 钳制）+ lease（虚拟时钟到期）+ compaction；随机 op；期望事件集与实际逐条比对。
**测**：watch 事件丢/重/乱序、watcher 泄漏、lease 到期、compaction 边界。
**不变量**：不丢不重、rev 严格递增、cancel 后集相等。

### n11_http_tls_conn — TLS/连接池（P1，L02/05/19）
httptest TLS server + 受控 Transport（限 idle/短超时/会话缓存）；确定性注入：3% Hijack/半截/垃圾、1/8 cancel-in-dial、1/8 连接级 25ms deadline、每 13 请求短 context；重试换 id。
**测**：persistConn/fd 泄漏、TLS 握手竞争、netpoll 竞态。
**不变量**：成功响应 id+crc32、失败可分类、收尾 idle 关闭 + 基线。

### n12_fuzz_decoder — fuzz 解码（P2，L05/06/13）
自生成 7 类语料（JSON/XML/gob/gzip/tar/zip/png）+ xorshift 变异（翻转/覆写/截断/插入/拼接），输入 ≤64KB、结构预算防 OOM；panic 只计数；成功路径做 roundtrip。
**测**：解码器 panic/死循环/错误路径泄漏。
**不变量**：JSON DeepEqual、gob 逐字段、panic rate 只记录。

### u01_wb_boundary_storm — 写屏障边界风暴（P0，L18/05/17）
老年代 4096 字段大 struct + 12288 项 slab 晋升后，每轮 5120 个 canary young 覆盖：单地址高频写、A/B 交替（并发 mark）、反向/自环/跨 slab、批量 copy/append、interface 装箱拆箱；分配后/GC 前/GC 后三段 magic 校验；GOGC 1↔50 抖动。
**测**：写屏障执行自身的边界与并发（区别于 n04 的"改边后存活性"）。
**不变量**：canary magic 三段校验、anchor 可达不终结。

### u02_preempt_scan_window — 抢占×栈扫描窗口（P0，L03/17/18）
三种持针形态 worker（接口方法热切换/方法值闭包链/聚合字段轮转）在极少调用的循环里，高频 GC 触发栈扫描 + GOMAXPROCS 1↔2（=1 时只剩 SIGURG 异步抢占，窗口最窄）；Gosched/channel/sleep 三种挂起点各持针；canary 全量校验 + anchor finalizer。
**测**：被抢占瞬间指针在寄存器/未 spill 槽的扫描正确性（区别于 s04 的"能否被抢占"）。
**不变量**：canary magic、挂起后指针内容、可达不终结。

### u03_gc_assist_pressure — GC assist 压力（P0，L05/18）
GOMEMLIMIT 动态贴近 live heap（1.0x/1.1x/1.2x 轮换），把分配风暴 worker 持续推进 GC assist（mutator 在分配点被拉去 mark）；临时对象"分配-校验-失活"窗口 + verifier goroutine；老年代 anchor 图并发 old→young 改边。
**测**：assist 路径下 mutator 栈状态与并发 mark（s02 只打堆循环，未打 assist）。
**不变量**：canary magic、anchor 全量校验、可达不终结。


### u04_unsafe_zerocopy_buffer — unsafe 零拷贝（P0，L07/17/18）
[]byte→string 零拷贝视图、slice header 拆装、内部指针（&buf[i]）作为唯一引用跨 GC；canary 三路径校验。
**测**：零拷贝别名生命周期、派生指针保活（L07 在 stress 层的专属场景）。

### u05_pool_identity_reuse — 池身份复用（P0，L05/18）
sync.Pool 大对象 Get 填充→Put→再 Get（复用时校验无旧指针残留）；外部持引用后 Put（GC 后外部读校验）；两级池 victim。
**测**：池化对象身份复用与回收竞态。

### u06_syscall_io_fs — syscall/IO（P1，L19/05）
并发 pipe 读写 checksum、临时文件 roundtrip、fd 复用跨 GC、syscall.Syscall 直调。
**测**：syscall 路径指针参数、fd 生命周期与 GC 交互。

### u07_goroutine_spawn_churn — goroutine 生成风暴（P0，L04/17）
每轮数百短 goroutine（defer/panic-recover/Goexit 各路径，持 canary 校验后退出）；持续 GC 扫退出中 goroutine。
**测**：栈缓存复用、退出窗口指针扫描。

### u08_panic_stack_interact — panic×栈交互（P1，L17/04）
深栈 panic→recover→再 panic 三层链；defer 闭包跨展开持指针；runtime.Error（nil map/越界/除零）深栈反复。
**测**：展开路径栈映射、defer/panic 帧。

### u09_stack_shrink_cycles — 栈收缩循环（P0，L04/17）
深→浅→强制 GC 收缩→再深的锯齿循环；收缩后残留帧区域指针校验（s03 打增长，u09 专打收缩）。

### u10_multi_gen_config — 多代配置热替换（P1，L02/18）
三代 config（map/slice/handler）原子换；in-flight 用旧代直到 drain 归零释放+GC。
**测**：旧代对象 UAF、换代原子性。

### u11_timer_heap_churn — timer 堆翻腾（P0，L19/04）
数十万一次性 AfterFunc（回调持 canary，到期校验）+ 长周期 timer Stop；堆 sift 高频。
**测**：堆大量增删、到期回调指针（区别 n01 的 Stop/Reset 语义）。

### u12_map_grow_stress — map 极限生长（P0，L13/18）
单 map 0→百万 entry 多次扩容搬迁 + 全程遍历抽查 + 后半程逐 key 删除 + 小比例重插（n02 是稳态翻腾，u12 是生长/搬迁路径）。


### x01_aggregate_statepoint — FCA/byval 聚合（P0，L17/05）
noinline 聚合传参/返回：`[2]*T`、`[2][2]*T`、嵌套 struct、>128B 混合 byval、goret 混排、8 种泛型实例化；callee 内 GC 后逐指针字段校验。
**测**：FCA 抽取嵌套数组（release 静默 miscompile 的 A4/A5）。

### x02_localarray_liveness — 局部指针数组（P0，L17/11）
`[N]T` 局部数组 + 非常量下标 + 取址跨 noinline call（call 内 GC）；8 形状矩阵；双路读校验。
**测**：go#82026 的 liveness/zero-point 形态。

### x03_namedret_defer_rvo — 命名返回/RVO（P1，L17/04）
命名返回 + defer 改写 + 大 struct 按值返回 + deferreturn 处 GC + 多返回路径 + 内联/非内联对照。
**测**：RVO 拷贝消除路径的 D19。

### x04_lockosthread_stw — LockOSThread/STW（P1，L04/17）
LockOSThread 递归 + 不 Unlock Goexit（oldm）+ ReadMemStats STW + finalizer 内 growStack。
**测**：锁线程下栈行为与线程退出路径。

### x05_goexit_panicframe — Goexit/panic 帧（P0，L17/18）
大 struct byval + 闭包取址局部数组 → 死栈对象形态 → GC → Goexit defer 链；三层 panic+Callers 走查。
**测**：go#82394/#82917 panic 记录保活 dead stack object。

### x06_coro_iterpull — coro 切换（P0，L01/16/17）
iter.Pull 双 goroutine 栈 + 每次切换间 GC + 嵌套 Pull + panic 跨 coro 传播 + LockOSThread 变体。
**测**：coro（m 栈切换）× GC 扫两个栈（30 场景此前零 coro）。

### x07_cgo_panic_deepstack — cgo 深栈 panic（P0，L09/17，需 cgo）
C pthread 回调 → Go 深递归 + GC → panic/recover → 回 C 再回调；GOMEMLIMIT 贴 live heap 强制 assist。
**测**：cgo extra M × 深栈 × panic 跨 C 边界 × assist。

### x08_signal_altstack — 信号 altstack（P1，L09/19，需 cgo）
C 自建 sigaltstack 回调 Go；Go 内 nil deref → sigpanic 穿 C 帧；SIGURG+SIGPROF 洪水；ucontext 换栈（洪水暂停防 hang，已注明）。
**测**：gsignal stktopsp / altstack 上的 sigpanic 展开。

### x09_asm_abi0_stackmap — ABI0 汇编 stackmap（P0，L08/17，需 asm/amd64）
手写 .s 带指针参数/返回值；asm 内 `CALL ·helper`（helper 分配+GC）指针活跨 asm 帧；asm 递归循环调 Go。
**测**：GoAsmFuncStackMap——汇编帧里调 Go 且指针跨调用（A8/P0#19）。

### x10_inline_tailcall_pcdata — inline/尾调用 pcdata（P0，L17/11）
内联 wrapper 链每层持针 + 最后一条 call 处 GC（工作函数首行 GC）；interface/method wrapper（musttail 候选）；noinline 对照。
**测**：last-call/deferreturn 的 pcdata 区间边界（A7/A12/D20）。

### x13_safepoint_matrix — 穷举注入矩阵（P0，L17/18/05，方法论核心）
12 种 IR 形态 shape × 每步 call 边界穷举单点/有界双点注入 GC；GOMAXPROCS=1 串行确定；失败打印 (shape,i,j,seed)；metrics 报"已覆盖点数/总点数"，未扫完不 PASS。
**测**：把"偶现 GC 落点"变成"每个 call 边界必测一次"。

### x14_weak_resurrect — weak/复活（P1，L18）
finalizer 复活再注册、AddCleanup 交错/Stop、weak.Make 强引用持有时必非 nil（活着被回收的直接判据）、interior/tiny。
**测**：终结器复活顺序与 weak 语义；配 finale profile。


### g01_goret_dirty_slot — goret 脏槽（P0，L17/18，源自 issue2）
issue2 核心复刻：命名大结果经 goret 内存槽返回 + 堆逃逸闭包经接口晚写入（写前闭包内 GC）；dirtyRec 递归刷脏栈后同深度下降使未初始化 alloca 落在脏字上；深度抖动 3+i%96。
**测**：goret 槽未初始化窗口（zeroGoRetArgs 修复针对的缺陷）。官方 Go 上为合法程序；CoreGoLLVM 未修复时预期 fatal error: found bad pointer / pointer to free object。

### g02_shrink_badpointer — 栈收缩坏指针（P0，L04/17/18，源自 issue1）
issue1 复刻：并发 goroutine 深调用→浅返回锯齿（中等复杂函数含指针局部+多返回+defer+非常量下标局部数组），GC worker 高频触发 shrinkstack→copystack→adjustpointers；GC 显式全走单 driver 串行化（window 下防 GC 握手死锁）。
**测**：adjustpointers 扫到非指针值（issue1 的 invalid pointer found on stack）。

### g03_goret_via_defer — defer 写入 goret（P1，L17，源自 issue2 变体）
命名结果由 defer 链写入（第一 defer 只 GC、第二才写结果）；defer 到期前即未初始化窗口；脏栈预热。
**测**：defer 形态的 goret 槽窗口。

### g04_escaped_result_heapaddr — 逃逸结果 heapaddr（P1，L18/17，源自 issue2 变体）
命名结果逃逸到堆（&res 经接口持有）；返回前 GC 扫调用方帧上的 heapaddr 槽（陈旧堆地址）；脏堆预热（同 size class 短命对象填埋后 GC 再调 victim）。
**测**：CoreGo Needzero 对应的堆逃逸结果槽路径。

### g05_multiret_deep_frames — 多返回×深帧（P1，L17/04，两 issue 组合）
多返回值（指针+聚合混合）× 深帧复用（脏栈 maker 同构下降）× GC 落在结果各字段写入间的每个边界（分字段逐步写入，每步 GC）。
**测**：结果写入窗口的穷举注入。

### g06_parallel_test_harness — 并发测试同构（P0，L02/17/18，源自 issue1 载体）
cel-go TestInterpreter 形态：数百"子测试"goroutine 并发跑同一深调用链（AST 构造+校验），波栅栏 gate 让整波在浅栈点集合统一 GC；gcChurn 常驻；GOMAXPROCS 阶段切换。
**测**：issue1 的 t.Parallel 并发 + GC worker 收缩窗口（GOMAXPROCS 大值时 GC worker 多）。


### y01_anon_ret_dirty_slot — 匿名返回脏槽（P0，L17/18）
x05 命中形状的最小纯化+压力拉满（无 panic 载体）：528B Big 匿名返回 + dirtyRec 同深度铺垫（d=3+i%96）+ 前/后双 GC 窗口 + 18 步分字段写入穷举 + 同栈位 4 轮复用 + chan/interface/weak 变体。
**测**：匿名（非命名）返回槽未清零——x05 命中的根因直接形状。

### y02_multiret_slots — 多返回并置槽（P0，L17）
`(Big,error)`/`(int,Big,*T)`/`(Big,Big2)`/`(Big,Big2,error)` 并置；早/晚/chan 消费交错；错误路径提前 return 时**其他结果槽未初始化**（潜在 UAF 窗口）+ 返回后零值校验。
**测**：多结果槽布局各自独立清零。

### y03_generic_ret_slot — 泛型返回槽（P0，L01/17）
`Box[T].Get() T` × 8 类型（112–288B 不同位图）× 直接/itab 字典/泛型局部三路；返回后 GC 再消费；脏栈铺垫。
**测**：单态化/字典双路径的返回槽。

### y04_rvo_escape_addr — RVO/内联消拷贝（P0，L11/17）
内联版（消拷贝直接构造在调用方槽）vs noinline 对照 + 返回值字段地址导出逃逸（&ret.p → 接口/chan）→ GC → 经导出指针校验；defer 修改返回值变体。
**测**：D19 RVO 消拷贝路径。

### y05_iface_tailret_slot — 接口/尾调用返回（P0，L17/06）
getter 接口 3 实现（值/复合/指针 receiver，含 264B 值 receiver 拷贝）itab 派发 + 方法值反复调 + 尾调用链层层穿透大返回值。
**测**：D20 尾调用 × 返回槽组合。

### y06_stackobj_reuse — 栈对象复用（P0，L17/04）
每层帧 3 个 stack object（`-m` 验证不逃逸）defer 闭包取址；GC 后逐层 defer 校验+weak；收缩 driver（GC/FreeOSMemory→copystack）；同栈区复用深调用。
**测**：A9 stackObjectRecord 正常流（此前从未覆盖）。

### y07_typedmemmove_gc — typedmemmove 穷举（P0，L18/17）
copy/拆分中间 GC/脏 dst+并发 GC/append 扩容/map 值拷贝+evacuation/chan 大值/iface 装箱/自搬运重叠/栈 dst/老→年写屏障，全路径逐字段 canary。
**测**：C17 含指针大值搬运（n04 只打 []*T 指针批量）。

### y08_global_gcprog_storm — 全局 gcprog（P1，L18/13）
36 种生成巨型全局类型（稀疏 pad/密集指针两种布局，总指针位 175 万）+ 并发改写+GC+复核 + reflect 链式构造打 typelinks + `reflect.New` 物化 gcdata。
**测**：B14/B15 gcprog/ptrdata + issue1002 路径。

### y09_param_slot_dirty — 参数槽（P1，L17/11）
内联后参数槽形态（&p 导出→GC→经导出读）+ defer 参数求值时点窗口 + 8+ 指针参数溢出栈传参区。
**测**：A8 参数槽（g 系列打 goret 结果槽，参数侧未打）。

### y10_mixed_ret_bombard — 组合终验（P1，L17/18/01）
y01/y03/y05 三形态 seed 轮换 + 每切换强制 GC+脏栈 + 高频 churn；分形态计数。
**测**：返回槽三形态混合终验。


### z01_keepalive_liveness — KeepAlive liveness（P0，L18/17/05）
KeepAlive 作为被测目标（go#30476/#22458/#16249）：可重物化栈地址 + 仅由栈对象引用的堆对象（引用链形态）；多栈对象数组；defer 中 KeepAlive；finalizer 提前触发=FAIL + weak 断言；深度抖动改帧布局。
**测**：对象被提前回收（liveness 缩短过度），而非栈槽脏。

### z02_uintptr_directive_forms — uintptrescapes 调用形态（P0，L17/18）
`//go:uintptrescapes` 5 形态矩阵（go#34474/#34684）：直接/方法值/方法表达式/函数值/接口方法；callee 内 runtime.GC 后解引用。实测：direct/method_expr 生效；method_value/func_value/iface_method 官方允许调用期内回收（接口方法上写指令被编译器拒绝），降级为强引用跨调用+内容校验。
**测**：指令未生效形态下的调用期 UAF。

### z03_reflect_makefunc_regabi — reflect MakeFunc RegArgs（P0，L17/06/11，requires reflect）
MakeFunc 签名 8+/12 指针参数（强制寄存器传播/溢出）；回调内 GC 后逐参数校验；变体：variadic、返回大 struct、嵌套 MakeFunc+Value.Call。
**测**：register-ABI RegArgs 假栈对象扫描（go#75778）。

### z04_licm_guarded_unsafe — 守卫内 unsafe 算术（P0，L11/17/07）
5 种守卫形态（边界/nil/类型/uintptr 中间量/string data）× 循环内周期 GC：指针算术在守卫内、基址循环不变量。
**测**：LICM 把指针算术提出守卫 → safepoint 上无效指针（go#82865/#83420，Go 1.27 新回归）。

### z05_opendefer_record_lifecycle — open-coded defer 记录（P0，L17/18/04）
递归函数 3–8 个简单 defer（open-coded）× 三层嵌套 panic/recover 链 × defer 内 GC；逐 defer 执行计数与参数校验。
**测**：panic 路径 defer 记录挂 g0/陈旧条目/参数槽 liveness（go#37688/#37782/#44623）。

### z06_cgo_newthread_stackgrowth — 新 C 线程+栈增长（P1，L09/04/18，requires cgo）
每轮新建 pthread 回调 Go；回调内 800–2000 层递归强制栈增长 + GC。
**测**：extra G 复用时的陈旧 gcstack（指向已 unmapped 栈段，gccgo 历史补丁）。

### z07_zerorange_mixed_frame — zerorange 区间（P0，L17/11/05）
>32KB 混合类型大帧（20+ 类局部）+ 分支条件初始化 + 帧中部强制 morestack；三态校验：未初始化==零值、已初始化==canary、指针槽非垃圾。
**测**：zerorange 清零区间错位（go#75477）。

### z08_escape_heap_to_stack — 逃逸分析失败（P0，L17/18/05）
五形态：闭包捕获逃逸、栈数组 append 后切片逃逸、interface 装箱局部指针、方法值捕获 receiver、泛型 `&v` 返回；finalizer+weak+canary。
**测**：堆持有栈指针（go#10353/#79592）。

### z09_init_time_stackgrowth — init 期栈增长（P1，L12/17/13）
三个 init：大帧+中段深递归/ mapassign 扩容与 morestack 交错 / 全局变量链顺序；main 长期读写复查。
**测**：init 帧指针映射（go#41816）。

## 运行

```bash
SUITE=stress ./run.sh                       # 默认 60s × soak profile
SUITE=stress PROFILES=soak,soak-single REPEAT=1 ./run.sh
SUITE=stress STRESS_SECONDS=5 FILTER=s02 ./run.sh
SUITE=all ./run.sh                          # unit + stress
```

### 夜间跑批

```bash
STRESS_SECONDS=3600 PROFILES=soak ./stress/run_nightly.sh
FILTER=s04 STRESS_SECONDS=3600 ./stress/run_nightly.sh
```

日志在 `build/nightly-<stamp>/stress/`；失败时打印 crash/core 路径。

`STRESS PLAN / RESEARCH / 矩阵`：`docs/STRESS_PLAN.md`、`docs/STRESS_RESEARCH.md`、`docs/STRESS_MATRIX.md`；新场景来源调研见 `docs/research/stress_scenarios_survey.md`。
