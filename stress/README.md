# stress —— 长跑压测层

与 `cases/` 的 19 维语义用例并列的**长跑层**，不占 19 维配额。当前 **30 个场景**（s01–s06 + n01–n12 + u01–u12），全部通过官方 Go 1.24.1 固定轮数 + soak 验证；s01–s06 另有 1h 基线。**u 系列专打 UAF**：canary magic + finalizer 探针 + clobberfree，让任何漏标当场现形。

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

- 场景内部要求：阶段化、持续不变量、心跳/watchdog、指标只记录不断言、结束 quiesce + goroutine 基线。

## 场景目录（30 个）

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
