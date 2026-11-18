# pgx v5.7.4

- 来源：research/repos/pgx（partial clone，仅 `*_test.go`、testdata、go.mod、README）
- 版本：v5.7.4（module `github.com/jackc/pgx/v5`，go.mod `go 1.21`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/pgx`
- 重点维度：L05 大量分配、L13 map/slice/字节缓冲、L18 GC 生命周期、L02 并发
- 说明：pgx 最可移植的是 `internal/iobufpool`、`internal/pgio`、`pgproto3`、`pgconn/ctxwatch`、`pgconn/internal/bgreader` 与 `pgtype` 的纯解析/Map 测试；凡出现 `PGX_TEST_DATABASE` 的场景只借思路。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| internal/iobufpool/iobufpool_test.go:10 | 按 256B/512B/…/32MB 尺寸类分配缓冲，>32MB 直接精确分配 | L05/L13 | 低 | 自制 size-class 池：2 的幂分桶 + 超大直接 make |
| internal/iobufpool/iobufpool_test.go:38 | Put 任意/错误尺寸缓冲后，Get 仍返回期望容量 | L05/L18 | 低 | 池 Put 时校验/归桶，错误尺寸丢弃；验证后续 Get 不受污染 |
| internal/iobufpool/iobufpool_test.go:71 | 100k 次 Get/Put，检测 GC 间隔下 buffer 是否被复用 | L05/L18 | 低 | sync.Pool 存指针切片，写标记字节后复用校验；穿插 GC |
| internal/pgio/write_test.go:23 | cap 足够的 buffer 上 AppendUint16 不新分配 | L05/L13 | 低 | 预分配 `[]byte` + `append` 大端编码；检查 len/cap |
| pgconn/ctxwatch/context_watcher_test.go:26 | Watch 后 cancel，HandleCancel/清理回调按序触发 | L02/L19 | 低 | context + 单次 goroutine，chan 收 cancel 回调 |
| pgconn/ctxwatch/context_watcher_test.go:52 | cancel 前先 Unwatch，两个回调都不应触发 | L02/L18 | 低 | 取消订阅状态机；取消后不得再触碰已释放对象 |
| pgconn/ctxwatch/context_watcher_test.go:104 | 100k 次 Watch/cancel/Unwatch 交错，atomic 统计取消与清理次数相等 | L02/L19/L18 | 中 | 交错循环 + atomic 计数；取消/清理配对校验 |
| pgconn/internal/bgreader/bgreader_test.go:49 | 后台预读未完成时 Read 等待，返回首段数据 | L02/L04 | 中 | 后台读 goroutine + 一次只允许一个后台读的握手 |
| pgconn/internal/bgreader/bgreader_test.go:114 | 随机读大小 1MB 总量，同时并发 Start/Stop 后台读 | L02/L04/L13 | 高 | 锁/chan 控制后台读启停，校验字节序与总量 |
| pgproto3/chunkreader_test.go:9 | 已缓冲数据不重复读底层 reader，耗尽后重置读写指针 | L13/L05 | 低 | 固定窗口缓存 + rp/wp，Next(0) 触发 reset |
| pgproto3/chunkreader_test.go:59 | 100k 次随机长度 1–16KB 取块，随机 reader 分片读取 | L13/L05 | 中 | 随机分片 reader + 边界块读取，校验长度与内容 |
| pgproto3/frontend_test.go:35 | 消息读一半中断，错误后再次 Receive 能恢复 | L18/L13 | 中 | 流式解码器：错误时保留状态，下一次从断点继续 |
| pgproto3/frontend_test.go:119 | 消息体长度超过上限返回 ExceededMaxBodyLenErr | L18 | 低 | 先读 4B 长度，超限立即失败且不分配大缓冲 |
| pgproto3/backend_test.go:64 | StartupMessage 合法/非法包长解析 | L06/L13 | 低 | 二进制长度前缀 + 参数 map 解析 |
| pgtype/array_test.go:8 | PostgreSQL 多维数组文本解析：引号、NULL、维度下界 | L13/L06 | 中 | 手写扫描器 + `[]string/[]bool/[]ArrayDimension` |
| pgtype/pgtype_test.go:527 | 编码计划缓存导致 UUID 数组类型混淆，错误应回退文本格式 | L06/L18 | 中 | 类型化 encode plan 缓存 + 错误回退，避免缓存串型 |
| pgtype/hstore_test.go:266 | hstore 文本/二进制编码→扫描往返，含 nil/空 map | L13/L06 | 中 | 手写 key/value 编解码 + map 往返 |
| pgtype/bytea_test.go:49 | 1000 行结果复用同一 byte slice，观察旧结果被覆盖 | L05/L18 | 中 | 行扫描复用缓冲；使用前拷贝验证失效语义 |
| pgtype/bytea_test.go:86 | PreallocBytes 保留原 cap，小结果不缩容、大结果再分配 | L05/L18 | 低 | 预分配缓冲复用策略：len 改写、cap 足够不重新分配 |
| rows_test.go:47 | RowScanner 错误后 Rows 进入 fatal 状态，后续读取报同一错误 | L18 | 低 | 扫描错误粘滞状态；错误后对象不可继续正常使用 |
| rows_test.go:112 | ForEachRow 回调中途 abort，剩余行不再消费 | L18 | 低 | 迭代中断/取消路径，释放资源 |
| batch_test.go:484 | 批量结果只读 3 行后 Close，再读下一批结果仍正确 | L02/L18 | 中 | 批量队列状态机：部分消费后推进到下一结果 |
| batch_test.go:1012 | 批处理出错后不遗留 orphaned prepared statement | L18 | 高 | 资源表 + 错误清理，校验无泄漏条目 |
| pipeline_test.go:12 | 无 prepared/describe 的 pipeline 连发两条查询并 Sync | L02/L13 | 中 | 请求队列 + 单次 flush + 顺序结果读取 |
| pgxpool/pool_test.go:111 | Acquire/Release 基本生命周期 | L02/L18 | 低 | 连接池 Get/Put 状态机 |
| pgxpool/pool_test.go:126 | Hijack 后连接从池中移除，池计数下降 | L18 | 中 | 借出/摘除状态，防止重复归还 |
| pgxpool/pool_test.go:469 | Release 时超过 MaxConnLifetime 的连接被销毁 | L18/L19 | 中 | 连接时间戳 + 惰性过期检查 |
| pgxpool/pool_test.go:496 | 有未读完结果（busy）的连接 Release 时被关闭 | L18 | 中 | 脏状态连接不可回池，必须销毁 |
| pgxpool/pool_test.go:717 | QueryRow.Scan panic 时连接仍被释放，Close 不挂死 | L18 | 中 | defer 归还 + recover 路径，验证池不泄漏 |
| pgxpool/pool_test.go:872 | 已显式关闭的连接 Release 后从池统计中消失 | L18 | 低 | 关闭标志优先于归还 |
| pgxpool/pool_test.go:904 | 100 goroutine 并发 Query/QueryRow，池自动扩缩 | L02/L04 | 中 | 并发连接池压测：上限、阻塞、唤醒 |
| conn_test.go:1312 | `RawValues()` 底层内存在后续查询中被复用覆盖 | L05/L18 | 中 | 结果缓冲复用契约：不拷贝则下次读取失效 |
| pgconn/pgconn_stress_test.go:16 | 10000 次随机 Exec/ExecParams/Batch，结束后 goroutine 数受限 | L02/L19/L18 | 高 | 连接状态机 + 随机动作压测 + 协程泄漏上限 |
| pgconn/pgconn_test.go:1165 | ResultReader 返回值的 cap 与 len 相等 | L05/L13 | 低 | 结果构造避免共享大底层数组；检查 cap==len |

## 推荐收录用例（按优先级）

| # | 优先级 | 建议用例名 | 来源 | 维度 | 重写要点 |
|---|---|---|---|---|---|
| 1 | P0 | `l05_iobuf_size_classes` | internal/iobufpool/iobufpool_test.go:10 | L05/L13 | 自建 size-class 池，覆盖 0/255/256/257/32MB/32MB+1 边界 |
| 2 | P1 | `l18_iobuf_pool_reuse_gc` | internal/iobufpool/iobufpool_test.go:71 | L05/L18 | sync.Pool Get/Put 循环 + GC 穿插，标记字节验证复用与清零 |
| 3 | P0 | `l13_iobuf_put_wrong_size` | internal/iobufpool/iobufpool_test.go:38 | L05/L18 | Put 错误尺寸缓冲，Get 必须返回正确桶容量；异常输入不污染池 |
| 4 | P0 | `l05_append_cap_noalloc` | internal/pgio/write_test.go:23 | L05/L13 | 预分配 cap 后 append 大端整数，len/cap 断言 |
| 5 | P1 | `l02_ctxwatch_cancel_order` | pgconn/ctxwatch/context_watcher_test.go:26 | L02/L19 | Watch→cancel→Unwatch，chan 收 HandleCancel，再校验 cleanup 顺序 |
| 6 | P0 | `l02_ctxwatch_unwatch_before_cancel` | pgconn/ctxwatch/context_watcher_test.go:52 | L02/L18 | 先 Unwatch 再 cancel，回调不得触发；已被释放对象不得访问 |
| 7 | P2 | `l02_ctxwatch_stress` | pgconn/ctxwatch/context_watcher_test.go:104 | L02/L19/L18 | 10 万次交错，atomic 校验 cancel 与 cleanup 成对 |
| 8 | P2 | `l04_bgreader_start_stop` | pgconn/internal/bgreader/bgreader_test.go:114 | L02/L04/L13 | 后台预读启停与随机大小读并发，校验字节序列 |
| 9 | P0 | `l13_chunkreader_buffered` | pgproto3/chunkreader_test.go:9 | L13/L05 | 固定窗口缓冲，已缓存不读源，Next(0) 重置 rp/wp |
| 10 | P1 | `l13_chunkreader_random_chunks` | pgproto3/chunkreader_test.go:59 | L13/L05 | 随机 reader 分片 + 随机取块，验证跨块内容 |
| 11 | P1 | `l18_frontend_interrupted_receive` | pgproto3/frontend_test.go:35 | L18/L13 | 读半条消息返回错误，补数据后再 Receive 成功 |
| 12 | P0 | `l18_frontend_max_body_len` | pgproto3/frontend_test.go:119 | L18 | 超长消息体立即失败，不分配大缓冲 |
| 13 | P1 | `l13_text_array_parse` | pgtype/array_test.go:8 | L13/L06 | 多维数组文本解析：引号/NULL/多维/下界 |
| 14 | P1 | `l06_encode_plan_cache_fallback` | pgtype/pgtype_test.go:527 | L06/L18 | 编码计划缓存 + 类型不匹配回退，避免 panic/串型 |
| 15 | P1 | `l13_hstore_roundtrip` | pgtype/hstore_test.go:266 | L13/L06 | map↔文本/二进制往返，含 nil/空/嵌入引号 |
| 16 | P1 | `l05_bytea_buffer_reuse` | pgtype/bytea_test.go:49 | L05/L18 | 行扫描复用缓冲：保存结果必须拷贝，否则被后续行覆盖 |
| 17 | P0 | `l18_rows_error_sticky` | rows_test.go:47 | L18 | 扫描错误后 Rows 粘滞失败，Close/Err 可判读 |
| 18 | P1 | `l02_batch_partial_read` | batch_test.go:484 | L02/L18 | 批量结果部分消费→Close→下一结果，队列状态不串 |
| 19 | P1 | `l18_pool_release_states` | pgxpool/pool_test.go:469,496,717,872 | L18 | 过期/脏/panic/已关闭四类连接 Release 必须销毁且不泄漏 |
| 20 | P2 | `l02_pool_concurrent_load` | pgxpool/pool_test.go:904 | L02/L04 | 100 goroutine 并发借还，上限/阻塞/唤醒统计 |

优先级说明：P0 为纯内存、确定性路径；P1 需要小规模并发或自建“结果缓冲”契约；P2 为压测规模。

## 不可移植清单

| 场景 | 代表位置 | 原因 |
|---|---|---|
| 需要 PostgreSQL 实例 | batch_test.go、conn_test.go、rows_test.go、values_test.go、pipeline_test.go、pgxpool/*、stdlib/*、pgtype 的 `RunValueRoundTripTests` | 依赖 `PGX_TEST_DATABASE` 与真实 SQL 执行/协议往返；本仓用例必须单包可独立运行 |
| 大对象/Large Objects | large_objects_test.go:15、:37、:164 | 依赖服务端 `lo_*` 函数与事务语义 |
| TLS/认证/证书 | pgconn/pgconn_test.go:99、:122、pgconn/config_test.go | 依赖测试证书、md5/SCRAM/GSS、环境变量与 libpq 默认值 |
| Windows/平台差异 | pgconn/config_test.go:46（skipOnWindows）、pgconn/pgconn_test.go:188 等 | 平台特定超时/TLS 行为，跨平台不可复现 |
| 断言实现细节而非契约 | conn_test.go:1312（RawValues 复用）、pgtype/bytea_test.go:49/86 | “底层缓冲会被覆盖/保留 cap”是 pgx 实现约定，stdlib 重写只能作为语义模拟，不应作为通用正确性要求 |
| 半内部/测试专用包 | internal/pgmock/pgmock_test.go、pgproto3 的 chunkReader 私有测试 | partial clone 下实现不可用；且依赖未导出类型 |
| 网络/超时/并发时序 | pgconn/pgconn_stress_test.go:16、pgxpool/pool_test.go:527 等后台检查 | 依赖 sleep/ticker 与真实网络，需改写为显式时钟/事件驱动 |

- cgo：未使用。
- runtime 内部结构：未直接依赖；但 `runtime.NumGoroutine` 断言需要固定测试规模，不能保留原 10000 次动作。
- 结论：优先收录 iobufpool/pgio/ctxwatch/bgreader/chunkreader/frontend/array/hstore 等纯内存场景；所有 `PGX_TEST_DATABASE` 场景只提炼状态机与缓冲复用思路。
