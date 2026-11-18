# etcd v3.5.0

- 来源：`research/repos/etcd`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / `README`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/etcd`
- 本地核验：`git describe` = `v3.5.0`，HEAD `946a5a6f`（2021-06-15，`version: 3.5.0`）
- 模块：`go.etcd.io/etcd/v3`，`go 1.16`
- 范围：仓库内 337 个 `*_test.go`。按 `go func|chan|select|sync|atomic|WaitGroup|timer|After|Ticker|Stop|Close|Cancel|NumGoroutine|leak|recur|pool` 先筛，再精读 `pkg/wait`、`pkg/schedule`、`server/mvcc`、`server/lease`、`raft`、`raft/rafttest`、`server/etcdserver/api/rafthttp`、`client/pkg/transport`、`pkg/adt` 等约 26 个文件；文件:行均相对本仓根目录。
- 说明：etcd 的运行级测试普遍依赖 bbolt 后端、真实 WAL 目录或集成集群。下表的“重写”只保留运行时机制（channel/select、锁、定时器、watch 取消、快照隔离、goroutine 泄漏），后端用内存 map + 版本号替代，不复刻协议。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| `pkg/wait/wait_test.go:23` | 单次事件注册/触发：`Register` 返回 channel，`Trigger` 写入并关闭，后续读得零值；重复触发被抑制（`:64`） | L02/L04 | 低 | `map[uint64]chan any` + `sync.Mutex`；触发时 send 后 close，二次触发直接返回 |
| `pkg/wait/wait_test.go:38` | 同一事件重复 `Register` 必须 panic；用 recover + channel 断言 | L02 | 低 | 注册表查重后 panic；goroutine 内 recover 后回传信号 |
| `pkg/wait/wait_time_test.go:54` | 10k 个 deadline channel 挂起，单次 `Trigger(10001)` 全部唤醒；每个等待 1s 超时 | L04/L02 | 中 | 有序 deadline + 桶计数，一个 `sync.Cond` 或一次性 close 广播；10k goroutine 阻塞/唤醒 |
| `pkg/schedule/schedule_test.go:22` | FIFO 调度器串行执行 100 个 job，`WaitFinish(100)`、`Scheduled()` 计数；`Stop()` 收尾 | L02/L04 | 中 | 单 worker goroutine + 有界 channel + `sync.WaitGroup`/计数；Stop 关闭队列并等待 in-flight |
| `client/pkg/testutil/leak_test.go:36` | `TestMain` 在全部测试后调用 `CheckLeakedGoroutine()`；样本故意起 100 个 `select{}` goroutine，期望检出泄漏 | L04/L02 | 中 | 记录基线 `runtime.NumGoroutine()`，用例结束轮询 10 次并抓 `pprof.Lookup("goroutine")`；可移植的泄漏断言框架 |
| `server/mvcc/watchable_store_test.go:55` | 建立同步 watcher 后 `Cancel`，断言 key 从 synced 集合移除 | L02/L18 | 低 | 订阅表 `map[key]map[id]chan Event` + RWMutex；取消时删表并 close channel |
| `server/mvcc/watchable_store_test.go:81` | 100 个 unsynced watcher（startRev=1）逐个 Cancel，要求 unsynced 集合归零 | L02/L13 | 中 | 两级 watcher 组（synced/unsynced）+ 索引 map；并发取消后校验 size==0 |
| `server/mvcc/watchable_store_test.go:140` | 100 个 unsynced watcher 经 `syncWatchers()` 批量迁移到 synced，每个收到一条 PUT 响应，stream channel 长度=100 | L02/L04 | 中 | 首次同步扫描历史版本，批量写 buffered channel；一边迁移一边消费 |
| `server/mvcc/watchable_store_test.go:400` | `watchBatchMaxRevs=4`，12 次 Put 被切成 3 批推送，每批 4 事件 | L13/L02 | 中 | 事件批量封包：累积到 N 条或超时即 flush；校验批边界与序号 |
| `server/mvcc/watchable_store_test.go:532` | 256 个 watch stream 各订阅同一 key，64 次并发 Put；channel 缓冲仅 1，触发 victim（慢 watcher）淘汰与通知 | L02/L04/L19 | 高 | 有界订阅 channel + 慢消费者检测（非阻塞 send 失败即标记 victim）；并发生产者/消费者，GC 穿插 |
| `server/mvcc/watchable_store_test.go:612` | 100 个 stream ×10 watch；每 stream 起 5 个 Cancel goroutine + 1 个 Close goroutine，同时 100 次 Put；要求无 panic/死锁 | L02/L04 | 高 | `sync.Once` 关闭 + 取消队列；Close/Cancel/Put 三方竞态，`-race` 下反复跑 |
| `server/mvcc/watcher_test.go:33` | 10 个同步 watcher + 10 个 unsynced watcher 的 ID 唯一性与事件归属，逐个 Cancel | L13/L02 | 中 | 单调 ID 分配器 + `map[WatchID]*sub`；同步/未同步两路径分别校验 |
| `server/mvcc/watcher_test.go:253` | 重复 Cancel 返回 `ErrWatcherNotExist`，取消队列清空 | L02/L13 | 低 | 取消操作幂等（第二次返回错误）；校验 pending cancel 列表长度 |
| `server/mvcc/watcher_test.go:290` | 对不存在/未同步 watcher `RequestProgress` 无响应；同步后返回带 revision 的进度响应 | L19/L02 | 中 | 进度请求走 channel；未就绪时丢弃，就绪后回送快照 revision |
| `server/lease/lease_queue_test.go:22` | 50 个租约按到期时间入最小堆，过期项轮询后回填重试队列；重试间隔 100ms | L13/L19 | 中 | `container/heap` + `map[LeaseID]time`；到期 poll、回填、按间隔重试 |
| `server/lease/lessor_test.go:105` | 一个 goroutine `Detach` 10 个 item，同时 10 个 goroutine 读 `lease.Keys()`，验证 itemSet 并发安全 | L02/L13 | 中 | `map[key]struct{}` + RWMutex；写删与全量遍历并发，结束后校验集合 |
| `server/lease/lessor_test.go:407` | 租约 1s 到期后从 `ExpiredLeasesC()` 取到；Renew goroutine 在 Revoke 前必须阻塞，Revoke 后返回 | L19/L02 | 高 | 定时器/到期 channel + 租约状态锁；Renew 等待 revoke 完成，select 超时判失败 |
| `server/lease/lessor_test.go:526` | 1s checkpoint ticker 周期性回调，回调 channel 在 2s 内被触发；`Stop()` 停 ticker | L19 | 中 | `time.Ticker` + `time.AfterFunc`；Stop 时 `ticker.Stop()` 并等待退出 |
| `server/mvcc/kvstore_test.go:653` | 长读事务创建后，写事务不得被阻塞；新读能看到新写，老读仍看到旧值（快照隔离） | L02/L18 | 中 | 读快照=不可变 slice/map 引用计数；写生成新版本，老快照持旧版本，GC 后校验 |
| `server/mvcc/kvstore_test.go:715` | 100 个写 goroutine（每个 1–10 次 Put）与 100 个读 goroutine 随机交错，读不得看到未提交写 | L02/L13/L18 | 高 | 版本化 map + RWMutex；提交后发布新快照，读侧原子取快照；GC 打断提交路径 |
| `server/mvcc/kv_test.go:411` | 写事务未结束时，并发 Put/Delete 必须阻塞；`txn.End()` 后 10s 内放行；Range 不阻塞（`:445`） | L02/L13 | 中 | `sync.RWMutex` 写锁跨事务持有；读走只读快照；goroutine + timeout 判阻塞/放行 |
| `raft/node_test.go:80` | 无缓冲 `propc` 上发起 Step，分别用 close(done) 与 context.Cancel 解除阻塞，返回 `ErrStopped`/`context.Canceled` | L02/L04/L19 | 中 | select 同时监听 done、ctx.Done、目标 channel；停止后保持幂等 |
| `raft/node_test.go:353` | node.run 主循环：ticker 驱动 Tick，Ready/Advance 推进状态，重复 AddNode conf change 去重，关闭 stop 后 goroutine 退出 | L02/L04 | 高 | 单 goroutine 事件循环 + select{tick, prop, recv, ready, stop}；Ready/Advance 双缓冲 |
| `raft/node_test.go:429` | 无 leader 时 Propose 阻塞；Campaign 成为 leader 后放行并返回 nil | L02/L04 | 中 | 提案队列 + leader 状态条件变量；proposal goroutine 等待状态切换 |
| `raft/node_test.go:525` | `Stop()` 等待 run 退出（done channel），重复 Stop 幂等；Stop 后 Status 为空 | L04/L19 | 中 | `sync.Once` + done channel；关闭顺序：停止入口→排空→关闭 done |
| `raft/rafttest/network_test.go:24` | 内存网络按 10% 概率丢包，1000 条消息后统计收包数在容差内（`:53` 为延迟注入） | L02/L19 | 中 | 每节点 inbox channel + 随机丢包/`time.AfterFunc` 延迟投递；固定种子保证可复现 |
| `raft/tracker/inflights_test.go:22` | 环形 buffer 的 Add/FreeTo/FreeFirstOne，含 start 回绕与满窗 | L13 | 中 | 泛型环缓冲（head/count/size）+ 掩码/取模；表驱动覆盖回绕边界 |
| `pkg/adt/interval_tree_test.go:359` | 随机插入唯一区间，对每个区间做 Stab/Intersects，再逐个删除至空；`Visit` 递归中序（`:406`） | L13/L04 | 中 | 自平衡区间树（或有序 slice+二分）；随机操作 10k 轮，递归 Visit 触发栈增长 |
| `server/etcdserver/server_test.go:1237` | 应用循环与快照发送并发交错 101 轮；快照索引落后应用索引时必须被拒绝/纠正，不得 panic | L02/L18/L19 | 高 | 内存快照 + applied index 原子推进；发送者与 applier 两个 goroutine 竞态，GC 插在快照拷贝前后 |
| `server/etcdserver/raft_test.go:156` | 有 pending apply 时向 `raftNode.stopped` 发停止信号，raft 循环必须在 1s 内退出并关闭 done | L19/L04 | 中 | 停止信号 channel + `select` 优先排空；WaitGroup/超时断言 |
| `server/etcdserver/api/rafthttp/pipeline_test.go:71` | 阻塞的 RoundTripper 下，消息缓冲满后第 N+1 个发送必须被拒绝/超时；`:217` 在满缓冲时 stop 仍须 1s 返回 | L02/L19 | 高 | 有界 channel + 非阻塞 send；stop 关闭 done 后 select 立即返回，处理 in-flight 不阻塞 |
| `server/etcdserver/api/rafthttp/stream_test.go:268` | stream writer/reader 经 HTTP 流互发消息，writer 未就绪时轮询 `writec()`，`stop()` 收尾 | L02/L19 | 高 | `net.Pipe` + bufio 帧格式；writer/reader 各一 goroutine，双向 channel 校验消息 |
| `server/etcdserver/api/v3compactor/periodic_test.go:29` | 周期压缩器：fake clock 推进，每 10 个 interval 触发一次 compaction，`Stop()` 停止循环 | L19 | 中 | `time.Ticker`（用例可注入 clock 接口）+ done channel；记录 compaction 次数/参数 |
| `client/pkg/transport/timeout_dialer_test.go:23` | 回环 TCP 上写 5MB 塞满 socket 缓冲区，带 10ms 写超时的 Dialer 必须返回 `i/o timeout`；读同理 | L19/L04 | 中 | `net.Listen("tcp","127.0.0.1:0")` + 只 accept 不读；对端填满缓冲后由写 goroutine 观察 deadline 错误 |
| `server/etcdserver/api/v3rpc/watch_test.go:26` | 大 WatchResponse 按 maxRequestBytes 切片，事件合并/单条超限/末片 Fragment=false | L05/L13 | 低 | 手写分片：按事件大小累积，超阈值即 send；用计数与末片标志断言 |
| `pkg/pbutil/pbutil_test.go:23` | 接口 `Marshaler`/`Unmarshaler` 的 Must 封装：成功透传，失败 panic（`:31`/`:41`） | L06 | 低 | 自定义 `type marshaler interface{ Marshal() ([]byte,error) }`；type switch/接口断言 + recover |
| `server/etcdserver/util_test.go:94` | `panicAlternativeStringer` 包装 `fmt.Stringer`，被包装者 panic 时回退到 alternative 文本 | L06 | 低 | 接口实现 + defer/recover 回退；类型 switch 区分 nil/正常/panic |
| `client/pkg/types/set_test.go:27` | 线程安全 Set 的 Add/Contains/Values/Length/Remove 全套；与 unsafe Set 共用同一套断言 | L02/L13 | 低 | `map[string]struct{}` + RWMutex；两实现跑同一测试函数，并发 Add/Values 校验 |

## 推荐收录（15 条）

1. `l04_wait_time_10k` — `pkg/wait/wait_time_test.go:54`。10k 等待者 + 单次广播，P1，主维度 L04，交叉 L02。缩到 2k 可做 P0。
2. `l02_watch_victims_fanout` — `server/mvcc/watchable_store_test.go:532`。256 订阅者、缓冲 1、64 并发写、慢订阅者淘汰；P2，L02/L04/L19。默认缩到 32×16，`TORTURE_*` 放大。
3. `l02_watch_cancel_close_race` — `server/mvcc/watchable_store_test.go:612`。100 stream 并发 Cancel/Close/Put；P1，L02/L04。建议在 `-race` 与 GC 交错下跑。
4. `l02_watch_unsynced_sync` — `server/mvcc/watchable_store_test.go:81`。100 个未同步 watcher 取消归零；P1，L02/L13。可与 `:140` 的批量同步合并为一个用例的两个阶段。
5. `l19_lease_expire_renew_race` — `server/lease/lessor_test.go:407`。到期 channel、Renew 阻塞到 Revoke；P1，L19/L02。用 100ms TTL + `time.After` 判超时。
6. `l02_lease_concurrent_keys` — `server/lease/lessor_test.go:105`。Detach 与 10 个 Keys 并发；P1，L02/L13。RWMutex map 的读写竞态。
7. `l19_lease_checkpoint_ticker` — `server/lease/lessor_test.go:526`。1s checkpoint 定时器与 Stop；P1，L19。可注入 tick 通道避免真实秒级等待。
8. `l02_txn_write_block` — `server/mvcc/kv_test.go:411`。写事务阻塞 Put/Delete、Range 不阻塞；P1，L02/L13。`sync.RWMutex` 跨事务持锁。
9. `l02_kv_concurrent_read_write` — `server/mvcc/kvstore_test.go:715`。100 读 + 100 写快照隔离；P2，L02/L13/L18。缩到 20+20，提交后强制 GC。
10. `l04_raft_node_stop` — `raft/node_test.go:525`。事件循环 Stop 幂等 + done 关闭；P0，L04/L19。纯 channel 版 node 状态机。
11. `l02_raft_block_proposal` — `raft/node_test.go:429`。无 leader 提案阻塞、Campaign 后放行；P1，L02/L04。条件变量/状态机。
12. `l19_raft_net_drop_delay` — `raft/rafttest/network_test.go:24`。内存网络丢包/延迟注入；P1，L02/L19。固定种子 + `time.AfterFunc`，校验容差。
13. `l04_goroutine_leak_detector` — `client/pkg/testutil/leak_test.go:36`。100 个永久阻塞 goroutine 必须被检测；P1，L04/L02。可作为其他用例的公共 leak 检查器。
14. `l19_tcp_write_deadline` — `client/pkg/transport/timeout_dialer_test.go:23`。socket 缓冲填满后的写/读超时；P1，L19/L04。回环 TCP，只 accept 不读。
15. `l13_interval_tree_random` — `pkg/adt/interval_tree_test.go:359`。随机区间插入/Stab/删除 + 递归 Visit；P1，L13/L04。缩到 2k 轮，Visit 递归制造栈压力。

## 不可移植清单

- **真实集群/二进制**：`tests/e2e/**`、`tests/integration/**`（成员启动、gRPC watch/lease/lock、网络分区、TLS、ctl 命令）、`tests/functional/**`（跨机 RPC/SSH）、`contrib/raftexample`。依赖真实端口、进程、集群成员与 RTT，只能把状态机降级为进程内 channel/net.Pipe 模型。
- **CLI/入口**：`server/main_test.go`、`etcdctl/main_test.go`、`etcdutl/main_test.go`、`server/embed/**`、`server/etcdmain/**`。依赖命令行参数、配置文件、真实监听与二进制路径。
- **磁盘/文件系统/WAL**：`server/wal/**`、`server/mvcc/backend/**`、`client/pkg/fileutil/**`、`pkg/osutil`、`server/snapshot/**`。依赖 bbolt mmap/fsync、flock、fallocate、磁盘满/坏块注入和文件权限，stdlib 无等价后端。
- **外部进程/信号/平台**：`pkg/expect/expect_test.go`（`/bin/echo`、`/usr/bin/printf`、`/usr/bin/tr`、`/bin/sleep`、SIGINT）、`pkg/netutil/routes_linux_test.go`（/proc/net/route）、`client/pkg/logutil/zap_journal_test.go`（systemd journal）、`client/pkg/fileutil/lock_linux_test.go`。依赖宿主工具与 OS 专属路径。
- **TLS/证书**：`client/pkg/tlsutil/**`、`client/pkg/transport/listener_test.go` 的 TLS 用例、`tests/integration/v3_tls_test.go`。依赖证书文件、私钥、cipher suite 与真实握手；只可移植“TLS 配置校验”的形式化分支。
- **认证/JWT/OIDC**：`server/auth/jwt_test.go`、`server/auth/store_test.go` 的 token 用例。依赖第三方 JWT 库、RSA/ECDSA 密钥与鉴权存储，stdlib 重写会丢失签名语义。
- **protobuf 生成代码**：`api/**/*_test.go`（如 `raft_internal_stringer_test.go`）、`server/etcdserverpb/**`。依赖 protoc 生成类型、gogo/protobuf 反射与注册表，不能放入独立 `package main`。
- **真实 HTTP/gRPC 传输**：`server/etcdserver/api/rafthttp/stream_test.go` 的 `httptest.Server` 用例、`tests/integration/clientv3/**`、`client/v3/example_*_test.go`。协议帧可借鉴，但真实 gRPC/HTTP2 流与 limiter 行为难以等价复现。
- **第三方 fake clock**：`server/etcdserver/api/v3compactor/periodic_test.go` 使用 `clockwork.FakeClock`。场景可移植，但代码本身不能脱离第三方库编译；建议用例改为接口注入 tick。
- **数据驱动 golden 文件**：`raft/confchange/datadriven_test.go`、`raft/quorum/datadriven_test.go`、`raft/*/testdata/**`、`server/wal/testdata/**`。依赖外部 testdata 与数据驱动框架；只能重写为程序内生成的表驱动用例。
- **构建标签/规模**：`tests/e2e/ctl_v3_watch_cov_test.go`（覆盖率构建）、`server/etcdserver/api/rafthttp/*norace*`、部分 `-race`/`-short` 分支。原样搬运会因标签或 337 文件规模在 CI 失配。
- **废弃 v2/v2store**：`server/etcdserver/api/v2store/**` 与 `tests/integration/v2store/**`。语义上可移植（TTL、watcher hub），但属于已废弃协议，优先级低于 v3 watch/lease。
