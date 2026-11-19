# 编译器/运行时稳定性长跑压测场景调研

> 日期：2026-11-19。范围：为 `stress/` 现有 s01–s06 寻找**不重复**的长跑形态；不改 `stress/`、`cases/`。
> 来源：`/mnt/d/work/Compiler/go`（下称 go）、`research/repos/`（下称 repos）、公开资料（§4）。
> 取舍：优先暴露 UAF/漏标/写屏障/栈映射/调度/泄漏/协议状态机；纯吞吐与性能基准不计。新增场景全部 stdlib（n07 需 cgo），不引三方依赖。

## 1. 候选场景与推荐顺序

表中 1–12 为推荐顺序（R1–R12）；`n` 编号是场景 ID，不表示排序。13–20 为备选或与现有场景重复，建议放弃/并入。`main.go 预估` 为手写行数，不含语料或生成器。

| # | 场景 | 来源（file:line / URL） | 形态 | 覆盖的失败模式 | stdlib | 优先级 | main.go 预估 | 结论 |
|---:|---|---|---|---|---|---|---|---|
| 1 | **n01 timer_netpoll_storm** | `go/test/stress/runstress.go:35-54`；`go/src/time/sleep_test.go:113-177,570-586`；`repos/quic-go/connection_timer_test.go:12-60` | 多 worker 持续 `Timer.Reset/Stop`、`AfterFunc` 重挂、ticker 多消费者、context 超时树；loopback TCP accept/dial/`SetDeadline`/早关；阶段切 GOMAXPROCS 1/2/4 + GC churn | timer 堆/丢失唤醒；netpoll 死锁；调度饥饿；fd/端口泄漏 | 仅 stdlib | 高 P0 | 450–600 | **R1** |
| 2 | **n02 map_table_churn** | `go/src/runtime/map_test.go:253-361`；`go/src/sync/map_test.go:159-219,314-358`；`go/test/stress/maps.go:32-102` | 分片 map 并发写/读；写中遍历、删除、`clear`；`sync.Map`；`reflect` MapKeys/MapIndex；int/string/struct/NaN key；遍历点插 GC | 扩容/搬迁；迭代器旧 bucket 生命周期；写屏障；哈希/NaN | 仅 stdlib | 高 P0 | 400–550 | **R2** |
| 3 | **n03 gc_lifecycle_finalizer** | `go/src/runtime/mfinal_test.go:24-160`；`go/src/runtime/mcleanup_test.go:119-159,249-282`；`go/src/weak/pointer_test.go:127-197`；`go/src/runtime/pinner_test.go:162-196`；`go/src/runtime/gc_test.go:805-903` | 对象注册 finalizer/`AddCleanup`/`weak.Make`/`Pinner`；分配-丢弃-GC 循环；finalizer 复活、interior pointer、tiny/zero-size；夜间 `checkfinalizers=1` | 终结器丢失/重复；cleanup 顺序；weak 置空时机；mark termination；pin 状态；tinyalloc | 仅 stdlib（Go 1.24+） | 高 P0 | 450–600 | **R3** |
| 4 | **n04 write_barrier_graph** | `go/src/runtime/gc_test.go:42-124,623-718`；<https://github.com/dotnet/runtime/blob/main/docs/design/coreclr/jit/investigate-stress.md> | 大老年代树/链表 + 年轻对象；worker 改 old→young 边、树旋转、批量 copy `[]*node`；并发 `runtime.GC`；夜间 `gccheckmark=1` | 漏标/写屏障；bulk barrier；栈映射；过早回收 | 仅 stdlib | 高 P0 | 400–550 | **R4** |
| 5 | **n05 trace_pprof_metrics** | `go/src/runtime/trace/flightrecorder_test.go:66-124`；`go/src/runtime/pprof/pprof_test.go:1621-1700,1840-1900,2253-2346`；`go/src/runtime/metrics_test.go:211-437`；`go/src/runtime/goroutineleakprofile_test.go:16-70` | 深栈/多 goroutine 负载背景；反复 `trace.Start/Stop` + FlightRecorder 并发快照；CPU/heap/goroutine/block/mutex profile；`pprof.Do` labels；`runtime/metrics` 周期读；goroutineleak profile | 栈回溯；SIGPROF 死锁；label race；trace buffer；profile 解析；泄漏判定 | 仅 stdlib | 高 P1 | 400–550 | **R5** |
| 6 | **n06 exec_signal_lifecycle** | `go/test/stress/runstress.go:84-113`；`go/src/os/exec/exec_test.go:616-657,1708-1776`；`go/src/runtime/crash_test.go:37-49,81-133`；`go/src/runtime/crash_unix_test.go:57-148` | 自 re-exec 多模式（正常/挂起/读 stdin/panic/SIGQUIT）；并发 fork/exec、pipe/ExtraFiles、context cancel；父进程校验 exit/output/crash 文本；fd/僵尸/goroutine 基线 | fork/exec fd 继承；pipe 泄漏；signal mask；SIGCHLD/wait；崩溃路径 | 仅 stdlib（Unix 优先） | 高 P1 | 400–550 | **R6** |
| 7 | **n07 cgo_callback** | `go/src/runtime/crash_cgo_test.go:62-120,170-206,315-344`；`go/src/runtime/testdata/testprogcgo/callback.go:51-114`；`go/src/runtime/pinner_test.go:356-445` | C 多线程回调 Go；回调内 `runtime.GC`、深递归、Go→C 传指针触发 cgoCheckPointer、Goexit；Go 调 C 阻塞；外部线程 SIGPROF；`NumCgoCall` 计数 | cgo 切换/extra M；GC hole；栈切换；信号转发；死锁 | 需 cgo | 高 P1 | 450–600 + `.c` | **R7** |
| 8 | **n08 parser_typecheck** | `go/test/stress/parsego.go:66-75`；`go/src/go/parser/performance_test.go:23`；`go/src/go/types/check_test.go:426-433` | 运行时生成大型合法 Go 包（深递归类型/泛型/接口）；循环 parse→types.Check→format；保留 AST/type info；插 GC | 深递归栈；AST/对象图漏标；FileSet/map 生命周期；前端内存 | 仅 stdlib | 中高 P1 | 500–700 | **R8** |
| 9 | **n11 http_tls_conn** | `go/src/net/http/transport_test.go:782-832,1864-1937,3953-4033`；`go/src/crypto/tls/handshake_client_test.go:2269-2320`；`go/src/net/dial_test.go:77-178`；<https://github.com/dotnet/runtime/tree/main/src/libraries/System.Net.Http/tests/StressTests/HttpStress> | server 随机 hijack/早关；client Transport 池复用、idle race、cancel-in-dial；TLS 握手竞争、session ticket/resumption；HTTP/2 GOAWAY/reset（可选） | persistConn/fd 泄漏；TLS 死锁；netpoll 竞态；协议状态机 | 仅 stdlib | 中高 P1 | 500–700 | **R9** |
| 10 | **n09 lsm_storage** | `repos/badger/levels_test.go:757-877`；`repos/badger/db_test.go:447-520`；`repos/badger/db2_test.go:577-676`；`repos/badger/manifest_test.go:254-290` | stdlib 小型 LSM（WAL+memtable+SSTable+level compaction+manifest+snapshot iterator）；并发写/读/迭代/compact/重开；temp dir | buffer 生命周期/UAF；旧版本迭代；compact race；fd/finalizer；大对象图 GC | 仅 stdlib（temp file） | 中 P2 | 600–800 | **R10** |
| 11 | **n10 mvcc_watch_lease** | `repos/etcd/server/mvcc/watchable_store_test.go:225-299,532-654`；`repos/etcd/server/lease/lessor_test.go:286-337,407-459`；<https://github.com/etcd-io/etcd/blob/main/tests/robustness/README.md>；<https://github.com/cockroachdb/cockroach/blob/master/pkg/kv/kvnemesis/doc.go> | revisioned KV + watcher（prefix/range/start rev/cancel/close）+ compaction + lease grant/renew/revoke/expire；随机并发 op + 模型检查；可注入 crash/partition | watch 事件丢/重/乱序；watcher 泄漏；lease 到期堆积；compaction UAF；goroutine 泄漏 | 仅 stdlib | 中 P2 | 600–800 | **R11** |
| 12 | **n12 fuzz_decoder** | `go/src/encoding/json/fuzz_test.go:15,55`；`go/src/archive/zip/fuzz_test.go:15`；`go/src/encoding/gob/codec_test.go:1423-1514`；<https://go.dev/doc/fuzz/> | 自写变异器 + 固定 seed 语料，循环喂 json/xml/gob/csv/gzip/tar/zip/png/url/template；recover + 输入/耗时上限；长跑统计 panic/hang | parser panic；死循环；OOM；截断流；错误路径泄漏 | 仅 stdlib（自写变异器） | 中 P2 | 450–600 | **R12** |
| 13 | n13 pubsub_broker | `repos/nats-server/server/client_test.go:1108-1300,1945-1999`；`repos/nats-server/server/sublist_test.go:118-304` | subject trie/wildcard、queue group、unsub race、slow consumer、per-client outbound queue | 订阅 map race/泄漏；消息丢/重；背压；内存增长 | 仅 stdlib | 中 | 400–550 | 备选：与 s06 重复度高（流水线/背压已覆盖），暂缓 |
| 14 | n14 quic_state_machine | `repos/quic-go/connection_test.go:1802-2024,2241-2300`；`repos/quic-go/connection_timer_test.go:12-60`；`repos/quic-go/internal/ackhandler/sent_packet_handler_test.go:422-700` | 内存 packet conn；packet number spaces、ACK/loss/PTO、flow control、stream 生命周期、conn ID 变更、idle timeout | timer reset storm；重传队列 UAF；状态机死锁；泄漏 | 仅 stdlib（模拟） | 中 | 550–750 | 备选：timer 并入 n01、连接并入 n11；完整重写成本高 |
| 15 | n15 hot_reload | `repos/caddy/admin_test.go:147-173`；`repos/caddy/listeners_test.go:24-117` | 原子换 config/handler、listener 启动/停止、in-flight drain、多代配置 | 旧 config/handler UAF；listener double close；goroutine 泄漏；请求丢失 | 仅 stdlib | 中 | 300–400 | 备选/合并：与 s01 生命周期重叠，作为 s01 变体 |
| 16 | n16 goroutine_lifecycle | `go/src/runtime/proc_test.go:401-431`；`go/src/runtime/stack_test.go:264-288,875-926`；`go/src/sync/pool_test.go:95-177` | 大量短 goroutine + defer/Goexit/panic-recover；栈缓存复用；Pool Get/Put | gFree/栈缓存；defer on Goexit；调度计数；泄漏 | 仅 stdlib | 低（重复） | +100–150（并入 s03） | 放弃独立：s03/s04 已覆盖栈/抢占，只把 Goexit 变体加进 s03 |
| 17 | n17 gc_pacer | `go/src/runtime/mgcpacer_test.go:16`；`go/src/runtime/debug/garbage.go:234`；.NET GCStress | 异构分配速率 + GOGC/GOMEMLIMIT 锯齿 + GC churn | pacer/assist；scavenger；heap goal；STW | 仅 stdlib | 低（重复） | +80–120（并入 s02） | 放弃独立：s02/s04 已覆盖 GC churn，把 GOGC/GOMEMLIMIT 模式加进 s02 |
| 18 | n18 channel_close_select | `go/src/runtime/chan_test.go:341,435,595,719` | close 并发 send/recv、nil channel、select 公平性、cancel 传播 | 关闭竞态；select 饥饿；sudog 生命周期 | 仅 stdlib | 低（重复） | +80–120（并入 s06） | 放弃独立：s06 已覆盖多级 channel/背压/取消 |
| 19 | n19 pool_sizeclass | `go/src/sync/pool_test.go:149-177`；`go/src/runtime/malloc_test.go:159,240` | 多尺寸/多 P Pool churn、tiny/large 对象混合、GC 清池 | size class；tinyalloc；Pool victim；释放后读 | 仅 stdlib | 低（重复） | +80–120（并入 s02） | 放弃独立：s02 已覆盖堆循环/FreeOSMemory |
| 20 | n20 serializer_roundtrip | s05/s01；`encoding/gob`、`text/template` | 大 struct/JSON/gob/template 往返 | 编解码字段丢失；别名/复用；模板状态 | 仅 stdlib | 低（重复） | — | 放弃：s01/s05 已覆盖 JSON/生成代码；gob/template 可作 s05 小变体 |

**重复度结论**：n13–n20 不新增独立目录。其中 n13/n14/n15 保留为备选；n16–n20 分别并入 s03/s02/s06/s02/s05，只加阶段或变体，避免场景爆炸。
**Go 源模式映射**：exec→n06；map→n02；goroutine 泄漏→n05/n06；GC 并发标记→n03/n04；trace/pprof→n05；dial→n01/n11；timer→n01；cgo→n07。
**repos 映射**：badger LSM→n09；nats pubsub→n13；quic-go timer/conn→n01/n11/n14；etcd watch/lease→n10。

## 2. 现有 6 + 新增 12 总表

| ID | 名称 | 核心形态 | 主要失败模式 | 状态 |
|---|---|---|---|---|
| s01 | service_soak | 进程内 HTTP/JSON、worker 客户端、map/ring 缓存、GC | 编解码、缓存指针、收缩后存活对象 | 现有 P1 |
| s02 | heap_cycle_soak | 指针密集 slab 环形保留 + FreeOSMemory + regrow | 零初始化、存活对象、堆收缩 | 现有 P1 |
| s03 | stack_churn_soak | 深递归/大帧/闭包/panic-recover | 栈复制、帧指针、展开路径 | 现有 P1 |
| s04 | preempt_storm_soak | 无调用紧循环 + unsafe 往返 + 并发 GC/GOMAXPROCS | 异步抢占、写屏障、指针目标 | 现有 P0 |
| s05 | generated_code_soak | 9.6k 行生成类型/方法集/dispatch/JSON | 大 switch、深拷贝、接口/字段 | 现有 P1 |
| s06 | queue_pipeline_soak | 多级有界 channel + 背压 + timer + map/slice 缓存 | 丢/重/乱序、指针内容、goroutine 泄漏 | 现有 P1 |
| n01 | timer_netpoll_storm | timer reset/stop/AfterFunc + dial/accept/deadline 风暴 | timer 堆、netpoll、调度饥饿、fd 泄漏 | 新增 P0 |
| n02 | map_table_churn | 并发 map 增长/遍历/删除/clear + sync.Map/reflect | 搬迁、迭代器、写屏障 | 新增 P0 |
| n03 | gc_lifecycle_finalizer | finalizer/cleanup/weak/Pinner + GC 循环 | mark termination、终结器、weak/pin | 新增 P0 |
| n04 | write_barrier_graph | old→young 图变异 + 树旋转/批量 copy + 并发 GC | 漏标、bulk barrier、栈映射 | 新增 P0 |
| n05 | trace_pprof_metrics | trace/flight recorder + 各类 profile + metrics | 栈回溯、SIGPROF、label/trace buffer | 新增 P1 |
| n06 | exec_signal_lifecycle | re-exec、并发 fork/exec/pipe、SIGQUIT/SIGCHLD | fd 继承、pipe/僵尸、signal/crash | 新增 P1 |
| n07 | cgo_callback | C→Go 回调 + GC/深栈/Goexit + 外部线程信号 | cgo GC hole、extra M、信号转发 | 新增 P1（需 cgo） |
| n08 | parser_typecheck | 生成 Go 包并循环 parse/typecheck/format | 深递归、AST 对象图、前端内存 | 新增 P1 |
| n09 | lsm_storage | WAL/memtable/SSTable/compaction/snapshot iterator | buffer UAF、旧版本迭代、compact race | 新增 P2 |
| n10 | mvcc_watch_lease | revisioned KV + watcher + compaction + lease | watch 丢/重/乱序、lease 堆积、泄漏 | 新增 P2 |
| n11 | http_tls_conn | 连接池/idle/cancel/早关 + TLS 握手竞争 | persistConn/fd 泄漏、TLS 死锁、netpoll | 新增 P1 |
| n12 | fuzz_decoder | 固定 seed 变异语料 + 多解码器长跑 | parser panic、死循环、OOM | 新增 P2 |

## 3. 落地建议

1. **先做 P0 四个**（n01–n04）：全部 stdlib、与现有 6 个正交，最直接覆盖 timer/netpoll、map/写屏障、GC 生命周期三个高发区；n01 可先复用 `runstress.go` 的 dial/exec 模式改造成有界长跑。
2. **再补 P1**（n05/n06/n07/n08/n11）：n05/n06 补“可观测性 + 进程/信号”盲区；n07 依赖 cgo，按 `requires: [cgo]` 默认不进 CI；n11 与 n01 共享 netpoll 压力，但重点在 TLS/Transport 状态机，避免合并。
3. **状态机场景放 P2**（n09/n10/n12）：n09/n10 代码量 600–800 行，建议二选一先做 n10（更贴 etcd watch/lease 的成熟缺陷模式），n09 后补；两者都应带随机 op 生成器 + 模型/不变量检查 + seed 回放。
4. **跨场景硬性要求**：沿用现有约定（`STRESS_SECONDS`/`STRESS_WORKERS`/`UAF_SEED`、心跳 watchdog、`STRESS PASS`、quiesce 后 goroutine/fd 基线）；新增“结束前做一次 quiesce + `pprof.Lookup("goroutineleak")` 检查”。
5. **确定性故障注入**（FDB/TigerBeetle 模式）：n09/n10/n11 增加可注入的 fake clock/net/disk 或 fault schedule，失败行打印 seed+phase+worker；不要做 wall-clock/绝对内存断言。
6. **GODEBUG 只作夜间模式**：`clobberfree=1` 常开；`efence` 仅短跑；`gccheckmark=1`、`checkfinalizers=1` 放 nightly；`asyncpreemptoff`/`gcstoptheworld` 作对照组。

## 4. 业界依据（Web）

| 来源 | 关键做法 | 用法 |
|---|---|---|
| [FoundationDB Simulation](https://apple.github.io/foundationdb/testing.html) | 单进程确定性模拟整个集群；注入机器/网络/磁盘故障；seed 精确重放；时间加速 | n09/n10/n11 的 fault schedule + seed 回放 |
| [TigerBeetle VOPR](https://github.com/tigerbeetle/tigerbeetle/blob/main/docs/internals/vopr.md) | stub clock/network/disk；丢包/重排/分区/磁盘损坏；大量断言与状态 checker；seed+commit 重放 | 状态机场景的 checker 设计；失败可复现 |
| [etcd robustness](https://github.com/etcd-io/etcd/blob/main/tests/robustness/README.md) | 随机负载 + watch/lease/compaction + crash/partition；已发现多条 watch/compaction/lease 真实缺陷 | n10 的直接形态来源 |
| [CockroachDB kvnemesis](https://github.com/cockroachdb/cockroach/blob/master/pkg/kv/kvnemesis/doc.go) | 随机并发 op + MVCC 历史 + 可串行化校验（Elle） | n10 的 op 生成器/校验器 |
| [OpenJDK GC stress](https://github.com/openjdk/jdk/blob/master/test/hotspot/jtreg/gc/TestGCALotAtAllSafepoints.java) / [JNI](https://github.com/openjdk/jdk/blob/master/test/hotspot/jtreg/gc/TestJNICriticalStressTest.java) / [ConcurrentGCBreakpoints](https://github.com/openjdk/jdk/blob/master/test/hotspot/jtreg/gc/TestConcurrentGCBreakpoints.java) | `GCALotAtAllSafepoints`、`JNICriticalStressTest`、`ConcurrentGCBreakpoints` | n04/n07 的 GC-at-safepoint/cgo 压力 |
| [.NET GC/JIT stress](https://github.com/dotnet/runtime/blob/main/docs/design/coreclr/jit/investigate-stress.md) / [48h GC stress](https://github.com/dotnet/runtime/blob/main/docs/workflow/testing/coreclr/gc-stress-run-readme.md) | `GCStress=0x1/0x2/0x4/0x8`：每次分配/JIT 指令/Preemptive 转换点触发 GC；48h 混合测试 | n03/n04 的“GC 高频插入”思路（Go 用 GOGC=1+显式 GC 近似） |
| [.NET HttpStress / SslStress](https://github.com/dotnet/runtime/tree/main/src/libraries/System.Net.Http/tests/StressTests/HttpStress) | 独立 HTTP/TLS 协议压力工程，长期跑连接/取消/错误路径 | n11 的形态参考 |
| [Rust Crater](https://github.com/rust-lang/crater) | 用新旧两版编译器构建/测试 crates.io 生态并比对结果 | 本仓 runner 级差分：stock vs CoreGoLLVM 跑同一 stress 二进制 |
| [Go x/tools stress](https://pkg.go.dev/golang.org/x/tools/cmd/stress) | 并行循环跑测试二进制，收集偶发失败 | 长跑/复现 harness |
| [Go fuzz](https://go.dev/doc/fuzz/) / [k6 soak](https://grafana.com/docs/k6/latest/testing-guides/test-types/soak-testing/) | 语料变异长期跑；恒定负载长时间跑并观察资源曲线 | n12 / 通用 soak 判定 |
