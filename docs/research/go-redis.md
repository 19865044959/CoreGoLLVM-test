# go-redis v9.8.0

- 来源：research/repos/go-redis（partial clone，仅 `*_test.go`、testdata、go.mod、README）
- 版本：v9.8.0（module `github.com/redis/go-redis/v9`，go.mod `go 1.18`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/go-redis`
- 重点维度：L05 大量分配、L13 map/slice/字节缓冲、L18 GC 生命周期、L02 并发
- 说明：go-redis 绝大多数顶层测试由 `main_test.go` 启真实 Redis（6380/6381/6390/6391 等）。可移植性最高的是 `internal/pool`（dummy dialer）、`internal/proto`、`internal/hscan`、`internal/hashtag`、`options_test.go`、`internal/internal_test.go` 及 `bench_decode_test.go` 的 stub conn 思路。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| internal/pool/pool_test.go:16 | ConnPool 基础：Get/Put 后统计 Hits/Misses/Idle/Total | L02/L18 | 低 | 手写固定容量连接池 + 计数快照 |
| internal/pool/pool_test.go:34 | MinIdleConns 连接尚未建连时 Close，池计数归零 | L18/L02 | 中 | 后台补连 goroutine + Close 取消，等待退出 |
| internal/pool/pool_test.go:71 | 池满时 Get 阻塞，Remove 一个连接后立即唤醒 | L02/L18 | 中 | 有界 slot + 等待队列；Remove 唤醒阻塞者 |
| internal/pool/pool_test.go:122 | MinIdleConns：创建即有闲连接，Get/Remove 后维持下限 | L02/L18 | 中 | 低频补充协程 + 幂等计数 |
| internal/pool/pool_test.go:290 | 10 goroutine×1000 次 Get/Put/Remove 混合竞态 | L02/L18 | 中 | 并发借还删压测，校验无重复/丢失连接 |
| internal/pool/pool_test.go:307 | Get/Put/Remove 并发不产生 data race | L02/L18 | 中 | 同上，重点在锁粒度与归还竞态 |
| internal/pool/pool_test.go:335 | 1000 并发 Get 不超过 PoolSize，闲连接为 0 | L02/L18 | 高 | 计数信号量 + 条件变量，上限断言 |
| internal/pool/bench_test.go:20 | 池大小 1/2/8/32/64/128 下并行 Get/Put 基准 | L02/L05 | 低 | `b.RunParallel` 对比不同容量的吞吐与分配 |
| internal/pool/conn_check_test.go:14 | 真实 net.Conn 活性检查：关闭/过期 deadline 判定 | L18/L19 | 低 | 本地 listener / net.Pipe，Read 探测连接健康 |
| internal/proto/writer_test.go:26 | WriteArgs 写 string/int/float/[]byte/bool/nil/time/net.IP | L13/L05 | 低 | bytes.Buffer 手写 RESP 编码，混合类型分派 |
| internal/proto/writer_test.go:93 | WriteBuffer 反复 Append 4 个参数的分配基准 | L05 | 低 | 复用 buffer 的 Append 基准，观察扩容 |
| internal/proto/reader_test.go:11 | RESP 全部类型（status/int/float/bool/bigint/error/nil/string/array/set/map）解析基准 | L13/L05 | 中 | bufio.Reader 手写解析器，覆盖各前缀与边界 |
| internal/proto/reader_test.go:71 | ReadLine 读 8KB 且只在末尾遇 CRLF | L13/L05 | 低 | bufio.Reader.ReadSlice 循环拼接，验证大行 |
| internal/proto/scan_test.go:25 | ScanSlice 将 []string 解到 []T 与 []*T | L06/L13 | 低 | 反射创建元素、json.Unmarshal 逐项填充 |
| internal/hscan/hscan_test.go:58 | 字段扫描：bad args、数值溢出边界、成功映射、时间格式 | L06/L13 | 中 | reflect 遍历字段 + strconv 边界，错误先于写入 |
| internal/hashtag/hashtag_test.go:17 | CRC16 标准向量 + 二进制 key | L13 | 低 | 查表 CRC16，固定向量校验 |
| internal/hashtag/hashtag_test.go:34 | HashSlot：`{tag}` 提取、空 key 随机槽、标签一致性 | L13 | 低 | 手写 tag 提取 + slot 计算；空 key 显式种子 |
| internal/internal_test.go:10 | RetryBackoff 指数退避 0..16 次且不超过上限 | L19 | 低 | 位移退避 + 上限钳制，逐次断言范围 |
| internal_test.go:86 | Ring SetAddrs 与 GetByKey 并发，持续 1s | L02/L18 | 中 | RWMutex 保护分片表，读写交错 |
| internal_test.go:196 | Ring 缩容关闭旧 shard、关闭后新建 shard 立即回收 | L02/L18 | 中 | 引用计数/关闭标志 + 幂等 Close |
| internal_test.go:302 | withConn：正常/超时/普通错误后连接是归还还是移除 | L18 | 中 | 借出→执行→归还状态机；超时连接必须销毁 |
| race_test.go:18 | 10 goroutine×1000 次 Echo/Incr/Set/MGet/大 value | L02/L05 | 中 | 并发客户端 + 大 value，需自建 mock 服务端 |
| race_test.go:107 | 4 goroutine×100 次读 1MB value，重连后继续读 | L02/L05/L18 | 中 | 大对象并发读 + 连接重建 |
| pipeline_test.go:13 | 普通/Tx Pipeline：空命令、Discard、Val/Err、自定义命令 | L02/L13 | 中 | 命令队列 + 批量 flush + 逐条结果 |
| pipeline_test.go:74 | 逐级增大到 15 条命令的 pipeline | L05/L13 | 低 | 变长批量写，校验结果数与类型 |
| pubsub_test.go:37 | 模式订阅状态机：subscribe→timeout→message→unsubscribe | L02/L19 | 高 | 订阅连接独占 + 有界消息 chan + 超时 |
| ring_test.go:143 | 动态缩/扩容 shard，复用旧 shard、关闭多余 shard | L02/L18 | 高 | 分片表热更新 + 连接生命周期 |
| ring_test.go:202 | Ring pipeline 分发 100 key；关闭后 Exec 返回错误不 panic | L02/L18 | 中 | 分片路由 + Closed 状态检查 |
| ring_test.go:709 | ClientPause 下 Tx/TxPipeline 超时 | L19 | 高 | 读写 deadline + 超时错误判读 |
| monitor_test.go:58 | Monitor 输出进 10 容量 chan，读 5 条后 Stop | L02/L19 | 中 | 后台读 goroutine + 有界 channel + 停止握手 |
| options_test.go:12 | URL 解析：DB/TLS/auth/超时/Unix socket/非法参数 | L06/L13 | 低 | net/url + map 解析，错误路径 |
| options_test.go:205 | ReadTimeout 归一化：-1/0/1/3 映射默认与纳秒 | L19 | 低 | 配置默认值/负值语义 |
| error_test.go:25 | ShouldRetry 分类：EOF/池超时/LOADING/READONLY/普通错误 | L19 | 低 | 错误类型/前缀匹配表驱动 |
| bench_decode_test.go:109 | stub conn 解码 RESP 并跑 server/cluster 两种路径 | L02/L05/L13 | 中 | 自建 net.Conn stub 返回固定字节，无外部服务 |
| bench_decode_test.go:275 | 动态/静态 goroutine 数（池 5/20/50/100）压测 | L02/L05 | 高 | 固定 worker 与按需 goroutine 对比，校验任务数 |

## 推荐收录用例（按优先级）

| # | 优先级 | 建议用例名 | 来源 | 维度 | 重写要点 |
|---|---|---|---|---|---|
| 1 | P1 | `l02_pool_blocking_get` | internal/pool/pool_test.go:71 | L02/L18 | 容量=N 的 pool 全部借出→再次 Get 阻塞→Remove 一个唤醒；chan 判活 |
| 2 | P1 | `l02_pool_size_limit` | internal/pool/pool_test.go:335 | L02/L18 | 1000 并发 Get，计数信号量保证不超过上限且不丢唤醒 |
| 3 | P2 | `l02_pool_get_put_remove_race` | internal/pool/pool_test.go:307 | L02/L18 | 借/还/删三路并发，校验连接不重复、不泄漏 |
| 4 | P1 | `l18_pool_min_idle` | internal/pool/pool_test.go:122 | L02/L18 | 后台补连维持下限，Get/Remove 后恢复；关闭时停止补充 |
| 5 | P1 | `l18_pool_close_safe` | internal/pool/pool_test.go:34 | L18/L02 | 建连阻塞中 Close，所有等待者退出，统计归零 |
| 6 | P0 | `l13_resp_readline_8k` | internal/proto/reader_test.go:71 | L13/L05 | 8KB 单行、CRLF 仅在尾部，循环 ReadSlice 拼接 |
| 7 | P1 | `l13_resp_parse_all_types` | internal/proto/reader_test.go:11 | L13/L05 | 覆盖 + - : , # ( _ ! $ * ~ > % | 等 RESP 前缀 |
| 8 | P1 | `l05_writer_append_args` | internal/proto/writer_test.go:26 | L13/L05 | 混合类型 WriteArgs，复用 bytes.Buffer，逐字节比对 |
| 9 | P1 | `l06_scan_slice_reflect` | internal/proto/scan_test.go:25 | L06/L13 | []T 与 []*T 两种目标，反射构造 + json 解码 |
| 10 | P1 | `l06_hscan_reflect` | internal/hscan/hscan_test.go:58 | L06/L13 | 字段扫描边界值/溢出/未知字段；先校验再赋值 |
| 11 | P0 | `l13_hashtag_slot` | internal/hashtag/hashtag_test.go:17,34 | L13 | CRC16 向量 + `{tag}` 提取 + 同 tag 同槽 |
| 12 | P0 | `l19_retry_backoff` | internal/internal_test.go:10 | L19 | 0..16 次退避单调且有上限，固定参数避免时间抖动 |
| 13 | P2 | `l02_ring_rebalance_race` | internal_test.go:86 | L02/L18 | RWMutex 分片表，SetAddrs 与 GetByKey 持续并发 |
| 14 | P2 | `l18_ring_shard_cleanup` | internal_test.go:196 | L02/L18 | 缩容关闭旧分片、关闭后新建分片立即回收；Close 幂等 |
| 15 | P1 | `l18_withconn_timeout_replace` | internal_test.go:302 | L18 | 借出连接执行；超时/坏连接移除、正常/业务错误归还 |
| 16 | P1 | `l13_pipeline_large_batch` | pipeline_test.go:74 | L05/L13 | 1..15 条命令逐级批量，结果数与类型一致 |
| 17 | P1 | `l02_ring_pipeline_closed` | ring_test.go:202 | L02/L18 | 分片路由后关闭，Exec 返回错误且不 panic |
| 18 | P2 | `l02_pubsub_state_timeout` | pubsub_test.go:37 | L02/L19 | 订阅→超时→消息→退订状态机，有界 chan，独占连接 |
| 19 | P2 | `l19_ring_tx_timeout` | ring_test.go:709 | L19 | 读写 deadline + 服务端暂停模拟，Timeout 错误可判读 |
| 20 | P2 | `l05_resp_decode_goroutines` | bench_decode_test.go:275 | L02/L05/L13 | net.Conn stub 返回固定 RESP，固定 worker 与动态 goroutine 对比 |

优先级说明：P0 为纯内存/无服务端；P1 需小规模并发或 mock 连接；P2 为时间/服务端状态相关压测。

## 不可移植清单

| 场景 | 代表位置 | 原因 |
|---|---|---|
| 需要 Redis 实例 | main_test.go:156、race_test.go:18、pipeline_test.go:13、pubsub_test.go:15、ring_test.go:143/202/709、monitor_test.go:58、probabilistic_test.go、doctests/* | 依赖 6380/6381/6390/6391/16600+ 等真实服务与 Redis 命令语义，不能单包独立运行 |
| Cluster/Sentinel/Enterprise | osscluster_test.go、sentinel_test.go、main_test.go（cluster/RECluster 场景） | 依赖多节点拓扑、故障转移与企业版命令 |
| 服务端时间/暂停控制 | monitor_test.go:58、ring_test.go:709（ClientPause） | 依赖 `MONITOR`、`CLIENT PAUSE` 等仅服务端可提供的状态 |
| 半内部/未导出实现 | internal_test.go:86、:196（ring.sharding/rebalanceLocked/closeCounter）、internal_test.go:302（withConn） | 直接调用包内未导出函数或断言内部字段，不属于稳定 API |
| 平台/系统调用 | internal/pool/conn_check_test.go:14（build tag linux/darwin/… + syscall.RawConn + httptest） | 平台相关；`syscall.Conn`/RawConn 在其他平台不可用 |
| 测试框架与时钟 | 全部 Ginkgo/Gomega 测试、`Eventually`/`time.Sleep`/`ReceiveTimeout` | 非 stdlib 断言框架且依赖真实时间，需改成显式事件/截止时间 |
| 实现细节断言 | bench_decode_test.go:109（stub conn 固定字节）、internal/pool 的 dummy net.Conn | 可作为思路来源，但不能把 stub 协议行为当成对外契约 |

- cgo：未使用。
- runtime 内部结构：未直接引用；但 pool/ring 测试大量依赖 goroutine 时序与内部计数，重写时要显式同步。
- 结论：go-redis 可稳定移植的是“连接池状态机 + RESP 编解码 + 反射扫描 + 分片路由”；所有真实 Redis/Cluster/Sentinel 场景只保留状态机模型，不直接复用测试。
