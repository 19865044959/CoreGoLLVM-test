# grpc-go v1.71.1

- 来源：`research/repos/grpc-go`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / 必要测试辅助源码）
- 模块：`google.golang.org/grpc`，`go 1.22.0`；测试基于 `grpctest`、`gomock`、`go-cmp`、`testify`，大量用例使用真实的 localhost TCP + HTTP/2 + 生成 protobuf stub。
- 本地路径：`research/repos/grpc-go`；HEAD `cdbdb759`，`git describe --tags` = `v1.71.1`。
- 范围：仓库内 332 个 `*_test.go`。先按 `concurrent|goroutine|leak|stress|shutdown|graceful|timeout|deadline|cancel|timer|select|channel|atomic|pool|buffer|reflect|interface|codec|flow|state|goaway|idle` 过滤，再精读 21 个文件：`internal/grpcsync`、`internal/buffer`、`internal/idle`、`internal/transport`、`internal/cache`、`internal/leakcheck`、`internal/profiling/buffer`、`mem`、`resolver`、`encoding` 及 `test/` 下的取消、优雅关闭、流清理、连接状态用例。文件:行均相对本仓根目录。
- 说明：`mem/`、`internal/grpcsync`、`internal/buffer`、`internal/cache`、`internal/idle`、`resolver`、`encoding` 为纯内存逻辑，可直接提炼；`internal/transport` 的单元测试使用 `net.Pipe`/回环 TCP 伪造传输层，可部分移植；`test/` 下的端到端用例依赖生成代码与真实网络，只借鉴状态机与竞态场景。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| internal/grpcsync/callback_serializer_test.go:37 | 100 个 goroutine 在锁内记录调度顺序并 `TrySchedule`，单消费者串行执行，要求执行序 = 调度序 | L02/L04 | 中 | `sync.Mutex` + 任务切片 + 单 worker goroutine；调度侧写带序号的 buffered chan，执行侧回传序号，最后比较两序列 |
| internal/grpcsync/callback_serializer_test.go:105 | 100 个并发 `TrySchedule`，每个回调 `wg.Done()`，必须全部执行且不丢唤醒 | L02/L04 | 低 | `sync.WaitGroup` + 有界/无界任务队列；主 goroutine `select` 等 `done` 与超时 |
| internal/grpcsync/callback_serializer_test.go:141 | 首个回调阻塞在 ctx 上时，后续回调不得越序执行；关闭后已入队回调仍须执行，新回调被拒绝并走 onFailure | L02/L19 | 中 | 队列 + “closed” 原子标志 + `context.WithTimeout`；关闭时先排空，再置位；`select` 区分 ctx.Done 与 done |
| internal/grpcsync/pubsub_test.go:58 | 两订阅者按序收到 10 条消息；订阅前发布的消息只回放最后一条；cancel 后 `Done` 关闭且不再回调 | L02/L04 | 中 | 互斥保护订阅者列表 + 每订阅者 buffered chan；保存 last message；ctx 退出时广播关闭 |
| internal/grpcsync/event_test.go:65 | `Fire` 幂等：首次返回 true，后续返回 false；Done channel 只关一次 | L02/L06 | 低 | `atomic.Bool` + `sync.Once` 关闭 channel；多 goroutine 并发 Fire 校验仅一个成功 |
| internal/buffer/unbounded_test.go:90 | 10 个 writer × 10 次 Put、1 个 reader 边读边 Load，读到的集合必须完整且不重复 | L02/L04/L13 | 中 | 切片/链表队列 + `sync.Cond` 或 channel 代理；核对 100 个元素 |
| internal/buffer/unbounded_test.go:125 | Close 后 Put 返回错误；关闭前已入队的元素仍可读出；重复 Close 幂等 | L02/L19 | 低 | `closed` 标志 + 尽数据流；用断言验证错误与 channel 关闭顺序 |
| internal/idle/idle_test.go:133 | 空闲超时定时器触发后，manager 调用 enforcer 进入 idle 模式 | L19 | 低 | `time.AfterFunc` 或可注入 fake timer；回调里置状态并由 channel 通知 |
| internal/idle/idle_test.go:346 | 空闲超时回调与 `OnCallBegin` 竞争，20 轮重复；两者都不得把有活跃 RPC 的连接误置 idle | L03/L19 | 高 | 原子 call 计数 + 定时器状态机；每轮并发起 5 个 RPC 与一次超时回调，循环中插入 `runtime.GC()` |
| internal/transport/transport_test.go:559 | 客户端 stream ID 耗尽后停止建流并排空在途流，不 panic、不泄漏 | L04/L19 | 高 | uint32 计数器 + “exhausted” 状态；耗尽后并发 NewStream 全部拒绝，等待在途完成 |
| internal/transport/transport_test.go:827 | GracefulClose 时保留唯一在途流；200 个 goroutine 并发建新流全部得到 ErrConnClosing；结束后 goroutine 泄漏检查通过 | L02/L04/L19 | 高 | 连接状态机 + RWMutex；关闭广播后新流拒绝；用 `runtime.NumGoroutine` 轮询验证 worker 退出 |
| internal/transport/transport_test.go:1606 | 5 条流并发 ping-pong（1MB 消息），校验每条流/连接级流控窗口的收支恒等 | L02/L13 | 高 | 每流与连接各一对 send/inflow 计数；并发读写后停掉 worker，离线断言 `limit = unacked + sendQuota` |
| internal/transport/transport_test.go:2821 | `Close` 返回必须等 reader goroutine 完成，避免 Close 后仍有读取回调 | L04/L19 | 中 | reader goroutine + `done` channel；Close 置位后 `<-done`，用超时保护死锁 |
| internal/transport/keepalive_test.go:52 | 空闲超过 MaxConnectionIdle 后服务端发 GOAWAY；有活跃流时不发 | L19 | 中 | `time.Timer` 活动时间戳；空闲回调触发关闭广播，另起 busy 分支验证不发 |
| internal/transport/keepalive_test.go:124 | 连接存活超过 MaxConnectionAge 发 GOAWAY，并留 grace 窗口 | L19 | 中 | 创建/宽限两级 timer；到期先广播 draining，再延迟强制关闭 |
| test/gracefulstop_test.go:101 | GracefulStop 关闭 listener；Close 被延迟时新连接被立即拒绝，RPC 返回 Unavailable | L02/L19 | 高 | 自定义 listener 包装：Close 阻塞到 Accept 返回；server 状态机区分 serving/draining/stopped |
| test/gracefulstop_test.go:217 | GracefulStop 必须阻塞到正在执行的 RPC 返回 | L02/L19 | 中 | 处理函数持 WaitGroup；GracefulStop 等待 `wg.Wait()`，主测试用短超时断言“尚未返回” |
| test/context_canceled_test.go:37 | 客户端 deadline 与服务器返回 PermissionDenied 竞争；用二分搜索 delay 制造两种结果，且 trailer 与状态码必须一致 | L03/L19 | 高 | context.WithTimeout 与一个写状态/trailer 的 goroutine 竞争；10 轮 × 二分 delay；断言状态与附属数据不撕裂 |
| test/context_canceled_test.go:126 | 取消与“压缩标志/编码名”两次 select 竞争，不能产生不一致头部 | L03/L19 | 中 | 两个独立 select 读同一个 header chan 与 ctx；强制调度让 ctx 分支后到；校验最终编码标志 |
| test/stream_cleanup_test.go:66 | 服务端发送超窗大消息后返回状态；流须在状态发送后才从 activeStreams 删除 | L18/L19 | 中 | 注册表 map + 引用计数；GC 后断言已完成流被移除、长流仍在 |
| test/clientconn_state_transition_test.go:58 | 单地址连接从 Idle→Connecting→Ready→TransientFailure 的状态迁移与回调 | L02/L06/L19 | 高 | 枚举状态机 + 观察者接口；fake resolver/balancer 通过接口回调推进状态，校验不跳变 |
| test/goaway_test.go:415 | 客户端 `Close` 与服务端 GOAWAY 并发，两者都不得 panic/死锁 | L02/L19 | 中 | 两个 goroutine 竞争关闭 + `sync.Once` 广播；用 done channel 与超时检测双向关闭 |
| mem/buffers_test.go:67 | 引用计数 buffer：`Ref` 后第一次 Free 不回收，第二次 Free 才调用池回收函数 | L18/L05 | 中 | `atomic.Int32` 引用计数 + `sync.Pool`；Free 到 0 时归还；GC 后确认无悬挂引用 |
| mem/buffers_test.go:128 | 二次 Free 必须 panic；Split/Ref after Free 等误用快速失败 | L18/L05 | 低 | 引用计数哨兵值 + panic 路径；表驱动覆盖 Free/Split/Ref 的非法时序 |
| mem/buffer_pool_test.go:51 | 池化 buffer 复用前清零；GC 可发生在 Get/Put 之间，测试需重试直到命中同一底层数组 | L05/L18 | 中 | `sync.Pool` + 定长 size class；Put 前 `clear`；循环重试规避 GC 清池 |
| rpc_util_test.go:351 | 解压后大小分别小于/等于/大于 maxReceiveMessageSize，以及解压器报错；输出 buffer 必须归还池 | L05/L06 | 中 | `io.LimitedReader` 计数 + `bytes.Buffer` 池化；表驱动覆盖边界与错误包装 |
| encoding/encoding_test.go:70 | 同名 compressor 重复注册，后注册者生效且名字列表去重；强制指定 codec 名 | L06/L10 | 中 | `map[string]Compressor` 注册表 + `sync.RWMutex`；接口 `Name()/Compress()/Decompress()`；重复注册覆盖 |
| resolver/map_test.go:74 | AddressMap 以 `Address` 为键；属性用 `Equal` 接口比较，BalancerAttributes 被忽略；Set 同键不增长 | L06/L13 | 中 | 结构体键 + 自定义 `Equaler` 接口；map 索引 + 线性属性比较；覆盖等价键/别名键 |
| internal/cache/timeoutCache_test.go:51 | 带 TTL 的 cache：Add 后到期自动删除并回调；Remove/Clear 不回调 | L19/L05 | 中 | `map` + 每项 `time.AfterFunc`；删除时 Stop timer；GC 后检查无残留 entry |
| internal/profiling/buffer/buffer_test.go:106 | 固定容量环形缓冲：串行 Push/Drain、溢出保留最新、双 goroutine 并发 Push/Drain 不丢不重 | L02/L13 | 中 | 原子读写下标 + `[]any` 环；溢出覆盖最旧；并发时用计数器校验总量 |
| internal/leakcheck/leakcheck_test.go:41 | 用 `runtime.Stack` 抓取 goroutine，过滤测试框架/白名单后报告泄漏数量；忽略注册项不计数 | L04/L19 | 中 | `runtime.Stack(all=true)` 解析 goroutine 头；白名单前缀；轮询等待退出 |
| benchmark/primitives/primitives_test.go:365 | 热循环中对 `any` 做接口类型断言，验证 itab 命中路径 | L06 | 低 | `any` 存实现类型 + 空接口断言；循环 N 次并统计成功次数，配合 GC 压力 |

## 推荐收录（15 条）

1. `l02_callback_serializer_fifo` — `internal/grpcsync/callback_serializer_test.go:37`。100 goroutine 调度 + 单消费者 FIFO；P1，L02/L04。
2. `l02_callback_serializer_close` — `internal/grpcsync/callback_serializer_test.go:141`。关闭时排空队列、拒绝新任务；P1，L02/L19。
3. `l02_unbounded_multi_writer` — `internal/buffer/unbounded_test.go:90`。10 写 1 读无界队列完整性；P1，L02/L04/L13。
4. `l03_idle_timer_rpc_race` — `internal/idle/idle_test.go:346`。空闲定时器与 RPC 起始 20 轮竞争；P2，L03/L19。
5. `l19_graceful_close_active_stream` — `internal/transport/transport_test.go:827`。GracefulClose 保留在途流、200 goroutine 建流被拒、goroutine 泄漏检查；P2，L02/L04/L19。
6. `l13_flow_control_accounting` — `internal/transport/transport_test.go:1606`。5 条并发流的流级/连接级窗口收支；P2，L02/L13。
7. `l19_gracefulstop_delayed_listener` — `test/gracefulstop_test.go:101`。GracefulStop + 延迟 Close listener + 新连接拒绝；P1，L02/L19。
8. `l03_cancel_status_race` — `test/context_canceled_test.go:37`。deadline 与状态写竞争，二分 delay + trailer 一致性；P2，L03/L19。
9. `l03_cancel_compression_select` — `test/context_canceled_test.go:126`。取消与压缩标志两次 select 竞争；P1，L03/L19。
10. `l02_clientconn_state_transition` — `test/clientconn_state_transition_test.go:58`。连接状态机 + 回调接口；P1，L02/L06/L19。
11. `l18_buffer_refcount_free` — `mem/buffers_test.go:67`。引用计数 buffer 最后一次 Free 才归还池；P1，L18/L05。
12. `l05_buffer_pool_clear` — `mem/buffer_pool_test.go:51`。池化 buffer 清零 + GC 穿插复用；P0，L05/L18。
13. `l19_timeout_cache_ttl` — `internal/cache/timeoutCache_test.go:51`。TTL cache 到期回调与 Remove/Clear 语义；P1，L19/L05。
14. `l06_codec_registry_iface` — `encoding/encoding_test.go:70`。接口注册表、重复注册覆盖、强制 codec；P0，L06/L10。
15. `l04_goroutine_leakcheck` — `internal/leakcheck/leakcheck_test.go:41`。goroutine 泄漏检测器自身；P1，L04/L19。

## 不可移植清单

| 测试文件:行 | 依赖 | 不可移植原因 |
|---|---|---|
| test/end2end_test.go:96 | 真实 TCP/TLS + HTTP/2 + 生成 protobuf stub | 依赖 `grpc_testing` 生成代码与网络往返；只能借鉴场景，不能作为单包 stdlib 用例 |
| credentials/tls_ext_test.go:51 | TLS 证书/ALPN/系统根证书 | 握手与证书链依赖 crypto/tls 环境与固定测试证书；非运行时机制 |
| internal/channelz/syscall_test.go:38 | Unix domain socket + `getsockopt` | 依赖 `x/sys/unix` 与 Linux socket 选项 |
| test/channelz_linux_test.go:30 | Linux `SO_*` socket 指标 | 仅 Linux，需要真实 socket 和 channelz 集成 |
| internal/transport/proxy_ext_test.go:78 | HTTP CONNECT 代理 + DNS + 环境变量 | 需要真实代理服务器与 resolver 行为 |
| authz/grpc_authz_end2end_test.go:291 | 真实服务端 + TLS/mTLS + 策略文件热加载 | 依赖 gRPC authz 引擎、文件监听与 TLS 身份 |
| xds/internal/xdsclient/pool/pool_test.go:38 | xDS protobuf + 管理面 | 依赖生成 protobuf 与 xDS 控制面语义 |
| interop/xds/custom_lb_test.go:48 | 外部互操作驱动 + xDS server | 需要外部进程/服务，非自包含 |
| benchmark/latency/latency_test.go:37 | 真实网络延迟测量 + 外部 benchmark server | 依赖网络与独立进程 |
| reflection/test/serverreflection_test.go:72 | gRPC server reflection + protobuf descriptor | 依赖反射服务与生成描述符，非 Go reflect 运行时机制 |
| encoding/proto/proto_benchmark_test.go:55 | protobuf 序列化 | 依赖 protobuf 运行时；可借用“编码缓冲复用”思路但不能直接搬 |
