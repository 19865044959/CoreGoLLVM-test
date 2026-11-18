# badger v4.5.0

- 来源：research/repos/badger（partial clone，仅 `*_test.go`、testdata、go.mod、README）
- 版本：v4.5.0（module `github.com/dgraph-io/badger/v4`，go.mod `go 1.21`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/badger`
- 重点维度：L05 大量分配、L13 map/slice/缓冲、L18 GC 生命周期/写屏障、L02 并发
- 说明：下表行号均为测试函数/关键语句真实行号；库的 skiplist arena、z.Buffer、写批次、流式写入、事务冲突与订阅死锁是最有复用价值的部分。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| batch_test.go:30 | `WriteBatch` 循环 Set 后统一 Flush；defer Cancel 后再使用 | L02/L18 | 中 | 用 slice 收集 K/V 后批量落 map；Flush/Cancel 状态机，错误路径后再 Flush 验证对象仍可用 |
| batch_test.go:157 | Managed 批次首条写入即失败，随后 Close，验证不 deadlock | L18 | 中 | 构造必失败写入，在错误路径释放资源并重试 |
| db_test.go:249 | 20 goroutine 各写 500 条，写完后单迭代器顺序校验 10000 条 | L02/L13 | 中 | WaitGroup + 分片 map/slice，写后全量有序校验 |
| db_test.go:1246 | 写协程经无缓冲 chan 交 key，读协程立即读取 | L02/L18 | 低 | chan 同步交接；读侧做值拷贝，验证不会读到复用内存 |
| db_test.go:2066 | 100µs ticker 并发 Sync 与 10000 条 128KB 大值写入 | L02/L05 | 中 | 后台 ticker 刷盘 + 大对象写循环，过程中穿插 `runtime.GC()` |
| db_test.go:1905 | 12 轮 Subscribe + `context.Cancel`，前后 `NumGoroutine` 相等 | L02/L19/L18 | 中 | 订阅 goroutine 退出路径 + cancel；用运行时协程数检测泄漏 |
| db_test.go:2677 | 10 个读协程循环 View，Close 后统一返回 "DB Closed" | L02/L18 | 中 | close 状态标志 + 读循环；关闭后错误必须可判读且不崩 |
| publisher_test.go:35 | 慢订阅回调阻塞，1110 个并发写不丢锁、不挂死 | L02/L19 | 高 | 有界 channel + 慢消费者 + `runtime.Gosched()` 排空 |
| managed_db_test.go:314 | DropAll 与 1ms ticker 持续提交并发执行 | L02/L18 | 高 | 双 map 原子交换 + 并发写错误计数 |
| txn_test.go:99 | 100 goroutine `CommitWith` 异步提交，另一读者校验总额恒定 | L02/L18 | 高 | 原子版本计数 + 乐观校验，提交回调与读快照共存 |
| txn_test.go:269 | 两事务读相同两账户后各写一个，后提交者冲突失败 | L02/L18 | 中 | 版本号 CAS/冲突检测，失败提交后对象可复用 |
| skl/skl_test.go:132 | 1000 goroutine 并发 Put，随后并发 Get 校验 | L02/L13/L18 | 中 | mutex 保护的有序 map/slice，读写并发生存性 |
| skl/skl_test.go:162 | 100 goroutine 各写 1MB value 到 120MB arena | L05/L18 | 中 | 大对象并发分配 + 存活 + GC |
| skl/skl_test.go:197 | 同一 key 100 写 100 读，至少一次读到合法值 | L02/L18 | 低 | `atomic.Value` 或 RWMutex，同 key 读写竞态 |
| y/y_test.go:44 | 512B 页缓冲跨页写 256B/1KB 并与 bytes.Buffer 对照 | L13/L05 | 低 | `[][]byte` 页表 + Bytes 拼接，跨页边界 |
| y/y_test.go:70 | 初始 32B 容量逐轮翻倍写入，逐轮对照 bytes.Buffer | L13 | 低 | `bytes.Buffer` Grow/Write 扩容对照 |
| y/y_test.go:301 | Allocator Reset 后 1M 次 Copy/append/protobuf 编码 | L05/L18 | 中 | `sync.Pool` 复用 `[]byte`，GC 前后校验池对象 |
| table/merge_iterator_test.go:162 | 4 路有序迭代器正/反向归并，重复 key 取后者 | L13 | 中 | `container/heap` 多路归并 + 去重 |
| trie/trie_test.go:28 | 前缀 trie 多前缀匹配，返回 id 集合 map | L13 | 中 | map 嵌套前缀树；返回结果拷贝 |
| stream_writer_test.go:373 | Prepare/Write 后 Cancel，随后错误用法不崩 | L18 | 中 | 显式状态机；Cancel 归还 buffer，重复 Flush 可判读 |
| stream_writer_test.go:405 | 10 个 stream 各写 KV + StreamDone 后 Flush，重开可读 | L02/L18 | 中 | 分批 channel + Flush + 持久化模拟 |
| stream_writer_test.go:582 | 10MB 大 value 经 10MB buffer 写入 | L05/L18 | 中 | 大对象 + 池化复用，写入后 GC 再读回 |
| value_test.go:207 | 100×32KB 大值写入，删 45 个后重写值日志并校验剩余 | L05/L18 | 中 | map 存大值 + 批量删除 + 全量重写 |
| db_test.go:1159 | PrefetchSize -10/0/1/10 均完整迭代 100 条 | L13/L05 | 低 | 批量预取窗口（含非法/零值回退） |
| db_test.go:447 | 压缩 DB 全量 StreamDB 到新目录后逐 key 校验 | L05/L13 | 中 | 流式编码/解码 + 分批复制，不保留源对象 |
| stream_test.go:174 | 3M KV、MaxSize 50MB 分批收集并按前缀计数 | L05/L13 | 高 | 有界缓冲 + 分批 flush + map 计数 |
| db_test.go:996 | 100k 事务异步提交后，3 goroutine 并发迭代同一 txn 快照 | L02/L18 | 高 | 共享只读快照 + 并发迭代器生命周期/双 Close |
| db2_test.go:205 | 65KB key、超 ValueLog 的 value 触发上限错误，再做大值 GC | L05/L18 | 中 | 边界长度校验 + 大对象分配/释放 |
| iterator_test.go:182 | 10000 key 前缀计数，覆盖 4 种缓存组合 | L13/L18 | 中 | 有序 key 前缀区间 + cache 开关模拟 |

## 推荐收录用例（按优先级）

| # | 优先级 | 建议用例名 | 来源 | 维度 | 重写要点 |
|---|---|---|---|---|---|
| 1 | P1 | `l18_stream_writer_cancel_reuse` | stream_writer_test.go:373 | L18 | 先 Prepare/Write 再 Cancel，随后 Flush/Write 必须可判读；验证取消后内部缓冲/对象仍安全 |
| 2 | P1 | `l18_batch_error_then_close` | batch_test.go:157 | L18 | 首条写入失败→Flush 报错→Close；覆盖错误路径后资源释放与再次使用 |
| 3 | P1 | `l05_pool_allocator_reuse_gc` | y/y_test.go:301 | L05/L18 | sync.Pool 复用 byte slice，Reset/编码循环中多次 `runtime.GC()`，校验长度与内容 |
| 4 | P0 | `l13_page_buffer_grow` | y/y_test.go:44,70 | L13/L05 | 多页缓冲跨页写、Truncate 后重写；与 bytes.Buffer 逐轮对照 |
| 5 | P1 | `l18_value_gc_large_values` | value_test.go:207 | L05/L18 | 32KB 大值批量写入、部分删除、全量重写；校验未删值仍存活 |
| 6 | P1 | `l05_skiplist_big_values` | skl/skl_test.go:162 | L05/L18 | 100 个 1MB 大对象并发写入+读取，穿插 GC |
| 7 | P1 | `l02_skiplist_concurrent` | skl/skl_test.go:132 | L02/L13 | 1000 写 + 1000 读并发；有序结构 + 读写锁，避免 data race |
| 8 | P0 | `l02_getset_chan_race` | db_test.go:1246 | L02/L18 | 无缓冲 chan 交接 key，写后立即读；验证值与所有权 |
| 9 | P1 | `l02_concurrent_write_iterate` | db_test.go:249 | L02/L13 | 20 写协程 + 单次全量有序迭代 |
| 10 | P1 | `l02_close_while_reading` | db_test.go:2677 | L02/L18 | 多读协程循环访问，主协程关闭；统一错误返回且无恐慌 |
| 11 | P2 | `l19_subscribe_goroutine_leak` | db_test.go:1905 | L02/L19/L18 | 订阅 goroutine + cancel 循环，比较 `NumGoroutine`（缩短 sleep） |
| 12 | P2 | `l02_publisher_deadlock` | publisher_test.go:35 | L02/L19 | 慢消费者阻塞回调 + 大量并发写；有界 channel 与调度让出 |
| 13 | P2 | `l02_dropall_write_race` | managed_db_test.go:314 | L02/L18 | 持续写入与整表交换/删除并发，统计错误但不崩 |
| 14 | P2 | `l02_txn_commit_async` | txn_test.go:99 | L02/L18 | 100 异步提交 + 并发读者校验不变量 |
| 15 | P0 | `l13_merge_iterator_4way` | table/merge_iterator_test.go:162 | L13 | 4 路有序 slice 正/反向归并，重复 key 取舍确定 |
| 16 | P0 | `l13_prefix_trie_match` | trie/trie_test.go:28 | L13 | 多前缀匹配返回 id 集合；含空前缀/重复 id |
| 17 | P1 | `l05_stream_writer_large_value` | stream_writer_test.go:582 | L05/L18 | 10MB value 经池化 buffer 写入、再读回；GC 后内容一致 |
| 18 | P2 | `l18_stream_batch_maxsize` | stream_test.go:174 | L05/L13 | 有界 MaxSize 分批收集 + 前缀 map 计数（缩小到 30 万条） |
| 19 | P0 | `l13_iterator_prefetch` | db_test.go:1159 | L13/L05 | 预取大小 -10/0/1/10 的合法性回退与完整遍历 |

优先级说明：P0 为无长循环、无文件持久化的确定性子集；P1 需小规模并发；P2 需要压测参数 `TORTURE_*` 放大。

## 不可移植清单

| 场景 | 代表位置 | 原因 |
|---|---|---|
| 内部结构断言 | db_test.go:2175（db.mt/db.imm/db.nextMemFid）、db2_test.go:715（db.vlog.filesMap）、levels_test.go:64（db.lc.levels） | 直接读写库私有字段与 LSM/值日志布局，属于实现细节，不能作为编译器用例契约 |
| 文件/WAL/manifest 格式 | backup_test.go:35、manifest_test.go:39、value_test.go:638、db_test.go:447（磁盘模式） | 依赖 badger 自有磁盘格式、目录与恢复流程；stdlib 无对应物，只能抽取“流式批量复制”逻辑 |
| 加密/压缩/KMS | value_test.go:555、key_registry_test.go:116、db_test.go:2213 | 依赖 AES 密钥环、zstd（klauspost）等三方实现，stdlib 不提供 |
| Windows/平台行为 | db2_test.go:670（TestWindowsDataLoss）、txn_test.go:849（ArmV7Issue311Fix） | 平台/架构特定缺陷回归，跨平台不可复现 |
| 外部三方对象 | publisher_test.go:35、stream_test.go、y/y_test.go:301 | 使用 ristretto 的 `z.Buffer/z.Allocator/z.Closer`、protobuf `pb.KV`、testify；用例不允许引入三方库 |
| 超大规模/内存 | db2_test.go:205（manual，65KB key + 超大 value）、db_test.go:996（100k txn）、stream_test.go:174（3M 条） | 原参数内存/时间不可控；仅可缩小参数重写 |
| 已禁用回归 | db2_test.go:577（TestL0GCBug 直接 Skip） | 不是可执行行为，不能作为来源 |

- cgo：未使用。
- runtime 内部结构：未直接引用；但大量测试依赖 badger 自己的 refcount/arena/memtable 生命周期，重写时只保留“对象存活与回收”语义。
- 结论：badger 适合作为 L02/L05/L13/L18 的“结构模拟”来源；磁盘、加密、备份、内部字段类场景不纳入。
