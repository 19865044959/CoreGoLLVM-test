# nats-server v2.11.0

- 来源：`research/repos/nats-server`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / `README`）
- 模块：`github.com/nats-io/nats-server/v2`，`go 1.23.0`（toolchain go1.23.7）
- 范围：仓库内 118 个 `*_test.go`。先按 `stress|concurrent|goroutine|pool|alloc|gc|timer|preempt|sched|atomic|chan|select|append|map|slice|shutdown|timeout|cancel|race|leak|stack` 做函数名/正文筛选，再精读 `server/`、`server/avl|thw|pse|gsl`、`conf/`、`test/` 下约 24 个与并发/内存/生命周期相关的文件；文件:行均相对本仓根目录。
- 说明：nats-server 测试普遍依赖本进程内 TCP 服务端与 `nats.go` 客户端。下表的“重写”只提炼运行时机制（channel/select、sync、pool、timer、finalizer、netpoll、GC 观测），不复刻 NATS 协议；可用 `net.Pipe` / 回环 TCP 保留 netpoll 语义。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| server/ipqueue_test.go:253 | 3 个生产者并发 `push` 300 个元素；单消费者 `select` 通知 channel、批量 `pop`、`recycle` 归还切片，断言 inProgress 归零 | L02/L04/L05 | 中 | 自写 `ipQueue[T]`：`sync.Mutex` + `[]T` + `chan struct{}` 单次通知 + `sync.Pool`；3 goroutine 生产、批量消费 |
| server/ipqueue_test.go:291 | 1000 个元素入队/出队/归还后，重复 5 轮验证切片容量被池复用；超过 max recycle size 的大切片不归还 | L05/L18 | 中 | `sync.Pool` + cap 阈值；`runtime.GC()` 后再次 Get，验证复用/淘汰，不依赖实现细节只验证不 panic 与容量上限 |
| server/ipqueue_test.go:350 | `drain()` 清空队列并把剩余底层切片还给池，同时消费掉通知信号 | L05/L18 | 低 | 带通知 channel 的 buffered queue，drain 后检查 len==0 且无残留信号 |
| server/sublist_test.go:897 | 一个 goroutine 连续插入 100 个队列订阅，同时主 goroutine 执行 1000 次 `Match`（禁用缓存）；随后对普通订阅重复 | L02/L04/L13/L17 | 中 | 手写三级 subject trie + `sync.RWMutex`；写者插入、读者递归匹配，匹配循环中周期性 `runtime.GC()` 覆盖栈扫描 |
| server/sublist_test.go:957 | 通配符订阅与队列订阅混合，两个 goroutine 各 10 次 `Match("foo.bar")` 并校验结果中的 subject/queue | L02/L13 | 中 | 不可变快照 + atomic.Pointer 发布，读侧无锁；GC 与高并发读交叉 |
| server/sublist_test.go:1127 | 订阅兴趣通知：RegisterNotification/ClearNotification，Insert 触发 `true`、重复 Insert 不重复通知，Remove 触发 `false` | L02/L19 | 中 | `map[string]chan bool` + 非阻塞 send；用 `select/default` 合并多次事件，验证通知合并语义 |
| server/norace_test.go:62 | 500 条 1MB 消息打向同一订阅者，10s 内必须全部送达且不标记 slow consumer | L05/L19/L02 | 高 | 1MB 定长 buffer 复用；有界 channel + 写超时；生产者/消费者各 goroutine，GC 与丢包策略 |
| server/norace_test.go:578 | 100 个短命 connection 各做 10 次 50KB request/reply；`runtime.GC()+debug.FreeOSMemory()` 后 HeapInuse 不得超过初始 3 倍 | L05/L18 | 高 | 每轮创建/关闭对象，批末强制 GC，采样 `runtime.MemStats.HeapInuse`；验证对象可回收、无隐藏引用 |
| server/norace_test.go:1009 | 对 client/route/gateway/leaf/websocket 五类监听器各并发拨号 200 次，15ms 后 Shutdown；已接受连接必须收到 EOF/错误而非超时 | L19/L04/L02 | 高 | `net.Listen("tcp","127.0.0.1:0")` + accept 循环；shutdown 关闭 listener 并写关闭信号；客户端设 2s deadline 区分“关闭”与“卡死” |
| server/norace_test.go:2570 | 200k 条 8KB 消息（3 个 subject、带 header）异步发布再全部消费；打印发布/消费耗时与内存（默认 skip 的长压测） | L05/L18 | 高 | 分批 5k 条 + 每批强制 GC；同步池化 payload；用 HeapAlloc/HeapInuse 观察峰值，TORTURE 放大 |
| server/norace_test.go:8101 | 5 个账号 × 1M 消息，比较 route pool_size=0/5；5 个发送者各自收到全部消息 | L02/L04/L05 | 高 | 5 个账号 goroutine + 每账号队列 + worker 池；缩至 100k 消息，GC 每 10k 次，atomic 计数校验总量 |
| server/norace_test.go:8483 | 50k 个客户端同时订阅，发布单条 48KB 消息，HeapAlloc 增长必须 ≤10% | L05/L02/L18 | 高 | 2k goroutine（缩规模）共享同一只读 buffer，仅传指针/切片头；GC 前后比较 HeapAlloc，验证广播不复制 payload |
| server/norace_test.go:10072 | 对 client/route/gateway/leaf/ws 全部连接对象设置 finalizer；全部关闭后每 10ms `runtime.GC()`，等待 finalizer 全部触发 | L18/L04 | 高 | `runtime.SetFinalizer` 标记结构体 + `runtime.GC()` 轮询；记录已回收对象，验证引用链断开（不强制要求全部触发） |
| server/norace_test.go:11455 | 消费者被人工 stall 时发布消息，生产者不得阻塞；关闭 no_fast_producer_stall 后生产者应被 stall 并输出告警 | L03/L19/L02 | 高 | 有界 channel + `select` 非阻塞发送；atomic 开关切换“允许丢弃/阻塞”；时间窗内检查消费进度与日志计数 |
| server/client_test.go:1304 | 发布 goroutine 连续发 10k 条，5ms 后另一连接 Unsubscribe，验证退订并发安全 | L02/L18 | 中 | 订阅表加 `sync.RWMutex`，投递路径持读锁；退订置 tombstone，GC 后确认旧订阅可回收 |
| server/client_test.go:1945 | 将客户端写 deadline 改为 1ns，100 条 1KB 消息触发 slow consumer；结束后服务器 client 数必须回到 0 | L19/L18 | 中 | `net.Pipe` + `SetWriteDeadline` 制造阻塞；关闭连接后检查注册表归零、对象无泄漏 |
| server/events_test.go:3173 | 账号连接数事件 timer 以 50ms 周期上报；本地 client 归零后 timer 必须停止，500ms 内事件数不超过 15 | L19/L18 | 中 | `time.AfterFunc`/ticker + atomic 计数；client 计数归零时 Stop，GC 后验证 timer 不可达 |
| server/rate_counter_test.go:21 | 100ms 窗口允许 10 次，随后 5 次被拒并计数；窗口过期后重新允许 | L19/L13 | 低 | atomic 计数 + 窗口时间戳，`time.Sleep` 跨窗口；检查允许/拒绝数 |
| server/thw/thw_test.go:22 | hash wheel 的 Add/Update/Remove/ExpireTasks/GetNextExpiration，覆盖“已过期任务”和空 wheel | L19/L13 | 中 | 时间轮或 `container/heap` + map；批量插入不同到期时间，推进时间并清理，检查 count |
| server/ring_test.go:22 | 容量 10 的环形缓冲 append 30 次，len 固定 10、totalConns 累计 30；另测元素顺序 | L13/L05 | 低 | 泛型 ring buffer（取模/双指针），覆写旧元素，校验 len/total 与遍历顺序 |
| server/avl/norace_test.go:33 | 生成 500 万随机 uint64 并 Insert；`debug.SetGCPercent(-1)` 后比较 SequenceSet 与 map 的 HeapInuse，要求 <2MB 且比 map 省 50 倍 | L05/L13/L18/L03 | 高 | 手写区间压缩集合（runs/bitmap），关闭 GC 采样 HeapInuse；缩至 100 万，结束时恢复 GC 并强制回收 |
| test/fanout_test.go:34 | 100 连接 × 100 订阅 = 10k 订阅，发布 500 条；每条消息在每个订阅的回调中必须按 0..499 顺序到达 | L02/L04/L05/L17 | 高 | 100 个订阅者 goroutine、各自有序队列；分发循环每 N 条 GC；校验单调序号与总回调数 5M |
| test/cluster_test.go:124 | 启动 2 个带 route 的服务器后关闭，`runtime.NumGoroutine()` 前后差值 ≤2 | L04/L19/L18 | 中 | 启动若干 accept 循环/worker，统一 context 取消 + WaitGroup；GC 后对比 goroutine 数并打印 stack |
| test/norace_test.go:295 | 15 节点集群组建完成，全过程 TotalAlloc 增量 <80MB | L05/L18 | 高 | 用内存 transport（chan/net.Pipe）模拟 15 节点，采样 TotalAlloc；缩至 8 节点，校验连接元数据不随节点数爆量 |
| server/jetstream_test.go:770 | Shutdown 与删除 100 个 consumer 并发；Memory/File 存储下均不得 panic | L02/L18/L19 | 高 | 共享对象的关闭 vs 删除竞态：`sync.Once`/状态机 + context；反复 100 轮，GC 与 -race 并跑 |
| server/jetstream_test.go:14078 | Memory KV 上多个 watcher 观察 put/delete/purge，校验更新序列与 pending 一致、无内存损坏 | L18/L13 | 高 | 带版本号的 slice 存储 + 订阅者 channel；删除/清空后校验 watcher 收到一致序号，周期性 GC |
| server/jetstream_test.go:21704 | 两个 goroutine 用 expected-last-subject-seq 并发发布同一 subject，校验最终序列 | L02/L18 | 高 | `sync.Mutex`/CAS 维护 per-subject 序号，双 goroutine 交替发布，GC 打断重试路径 |
| server/parser_test.go:815 | 限制 max control line：分片输入与完整输入都必须拒绝超长 PUB；route/leaf/gateway 类型放行 | L13/L05 | 中 | `bufio.Scanner` 风格分片解析器：维护跨块缓冲，超限返回错误；表驱动不同输入长度 |
| conf/lex_test.go:588 | 多行数组/嵌套 map 的词法状态机，逐 token 校验行列号与注释跳过 | L13/L05 | 低 | 手写 lexer：状态枚举 + token slice，输入长配置后 GC；校验 token 流与位置 |
| server/filestore_test.go:6363 | PurgeEx 后消息块 cache 只保留 1 个已加载块，防止批量清理把全部块读入内存 | L05/L18 | 中 | 用内存 block cache 模拟：大量 block + 批量删除，检查 LRU/引用计数只保留一个热块 |
| server/memstore_test.go:1387 | 1M 次 SkipMsg + 2 次 StoreMsg 后，带通配符的 NumPending 扫描性能 | L05/L13 | 中 | 百万级序号空洞 + 惰性区间结构；缩至 20 万，循环查询并强制 GC，衡量放大系数 |
| server/routes_test.go:2079 | route pool 建立过程中并发连接/关闭，覆盖握手与池化竞态 | L02/L19 | 中 | 连接池 + accept/拨号竞态；`net.Pipe` 模拟半开连接，并发 Close，检查 pool 不泄漏 |
| server/gateway_test.go:3616 | gateway 对端服务器关闭的同时，客户端循环 QueueSubscribe/Unsubscribe，验证无 panic/卡死 | L02/L19/L18 | 中 | 订阅表 + 连接状态机；关闭 goroutine 与订阅循环并发，GC 后检查订阅表为空 |
| server/signal_test.go:428 | lame-duck 模式下已连接客户端收到断开；随后 TERM 信号不得提前终止进程 | L19/L04 | 高 | 仅借鉴状态机：`os/signal` 在 Linux 下可控，但需构建标签；更稳妥用 context 模拟“优雅期+强制期”两阶段关闭 |
| test/service_latency_test.go:1226 | service import 与 stream import 交叉时的请求路由（原栈溢出回归用例） | L04/L17 | 中 | 递归解析导入链并设深度上限；构造多层转发，GC 打断递归，校验不 panic 且有界 |

## 推荐收录（18 条）

1. `l02_ipq_multi_producer` — `server/ipqueue_test.go:253`。三生产者/一消费者 + 通知 channel + 切片池；P1，主维度 L02。
2. `l05_pool_slice_recycle` — `server/ipqueue_test.go:291`。池化切片容量阈值与 GC 后复用；P1，L05/L18。
3. `l02_sublist_concurrent_match` — `server/sublist_test.go:897`。并发插入/匹配 + 递归 trie + GC；P1，L02/L04/L17。
4. `l18_interest_notify_channel` — `server/sublist_test.go:1127`。兴趣变化合并通知（非阻塞 channel）；P1，L18/L02。
5. `l05_slow_consumer_1mb` — `server/norace_test.go:62`。1MB × 500 背压；P2，L05/L19。
6. `l18_route_mem_after_churn` — `server/norace_test.go:578`。连接 churn 后 `runtime.GC()+FreeOSMemory` 的 HeapInuse 上界；P1，L18/L05。
7. `l19_accept_shutdown_dial` — `server/norace_test.go:1009`。监听器关闭与并发拨号，EOF/超时区分；P1，L19/L04。
8. `l05_route_pool_longrun` — `server/norace_test.go:8101`。5 账号 × 长循环投递，验证 pool/队列/GC；P2，L05/L02/L04。
9. `l05_broadcast_no_copy` — `server/norace_test.go:8483`。大规模订阅者共享只读 payload，HeapAlloc 上限；P2，L05/L18。
10. `l18_finalizer_conn_release` — `server/norace_test.go:10072`。finalizer + GC 轮询验证连接对象释放；P2，L18。
11. `l03_fast_producer_stall` — `server/norace_test.go:11455`。有界队列 + 非阻塞发送 + 运行时开关；P1，L03/L19。
12. `l19_event_timer_lifecycle` — `server/events_test.go:3173`。timer 随计数归零而停止；P1，L19/L18。
13. `l05_seqset_mem_vs_map` — `server/avl/norace_test.go:33`。百万级区间集合 + 关闭 GC 采样内存；P2，L05/L13/L18。
14. `l02_fanout_ordering` — `test/fanout_test.go:34`。10k 订阅 × 500 消息有序扇出；P2，L02/L04/L05/L17。
15. `l04_shutdown_goroutines` — `test/cluster_test.go:124`。关闭后 goroutine 数回落；P1，L04/L19。
16. `l18_shutdown_delete_race` — `server/jetstream_test.go:770`。关闭 vs 删除并发；P1，L02/L18/L19。
17. `l13_memstore_interior_delete` — `server/memstore_test.go:1387`。百万序号空洞 + 通配符扫描；P2，L05/L13。
18. `l19_timer_wheel` — `server/thw/thw_test.go:22`。时间轮增删改与到期批量回收；P1，L19/L13。

## 不可移植清单

- **TLS/证书/OCSP**：`test/tls_test.go`、`server/routes_test.go` 的 TLS 用例、`server/leafnode_test.go` 的 TLS 用例、`test/ocsp_test.go`、`test/ocsp_peer_test.go`、`server/websocket_test.go`。依赖真实 TLS 握手、证书链、OCSP responder 或本地证书文件，stdlib 重写无法等价覆盖。
- **多节点真实网络/RTT**：`test/cluster_test.go` 的多数用例、`server/routes_test.go`、`server/gateway_test.go`、`server/leafnode_test.go`、`test/new_routes_test.go`、`server/jetstream_cluster_*_test.go`、`server/jetstream_super_cluster_test.go`。依赖真实端口、进程重启、跨节点 RTT 和网络分区；只能把状态机降级为进程内 `net.Pipe`/channel 模型。
- **JetStream 文件存储**：`server/filestore_test.go`、`server/dirstore_test.go`、`test/...` 中带 `StoreDir` 的用例。依赖 fsync、块文件布局、磁盘错误注入，不适合仅在内存中复现。
- **MQTT/WebSocket 协议栈**：`server/mqtt_test.go`、`server/mqtt_ex_test_test.go`、`server/websocket_test.go`。依赖 MQTT/WebSocket 帧与握手实现，属于协议移植而非运行时机制测试。
- **JWT/nkeys/Operator/LDAP/auth callout**：`server/jwt_test.go`、`server/auth_callout_test.go`、`server/nkey_test.go`、`internal/ldap/dn_test.go`、`server/opts_test.go` 的 resolver 用例。依赖外部 jwt/nkeys/ldap 语义或第三方库。
- **平台/OS 专属**：`server/signal_test.go`（SIGUSR2/kill/pgrep）、`server/pse/*`（/proc、`ps`）、`logger/syslog_test.go`（Unix syslog）、`server/tpm/**`、`server/certidp`、`server/certstore_windows_test.go`、`server/service_windows_test.go`。含平台 syscall、系统进程或硬件依赖。
- **监控 HTTP 端点**：`server/monitor_test.go`。依赖 `/varz`、`/connz` 等 HTTP 服务，仅可借鉴并发快照思路。
- **真实客户端压测**：`test/bench_test.go`、`server/core_benchmarks_test.go`、`server/benchmark_publish_test.go`。依赖 nats.go 客户端与真实 TCP 往返，属于吞吐基准而非稳定性用例。
- **配置重载/信号**：`server/reload_test.go`、`server/signal_test.go`。依赖 SIGHUP + 配置文件写入时序。
- **构建标签依赖**：`server/jetstream_*` 受 `!skip_js_tests` 等标签控制，`server/norace_test.go`、`test/norace_test.go`、`server/avl/norace_test.go` 受 `!race` 控制；原样搬运会因标签/规模在 CI 失配，需按场景重写而非复制文件。

## 备注

- 最值得优先落地的是 L02/L19 的“有界队列 + 通知 channel + 关闭广播”组合（ipqueue、accept shutdown、fast producer），可在纯 stdlib 下稳定复现。
- L18 的关键观测手段是 `runtime.GC()` + `runtime.ReadMemStats`/finalizer，NATS 中 `RouteMemUsage`、`ConnectionObjectReleased`、`ClientOutboundQueueMemory` 三个场景给出了很好的阈值设计。
- L17 不是显式断言维度，本库适合用“递归 subject 匹配 + goroutine 并发 + GC 打断”构造栈扫描压力，例如 `server/sublist_test.go:897`。
