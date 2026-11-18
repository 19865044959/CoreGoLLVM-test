# quic-go v0.50.1

- 来源：`research/repos/quic-go`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / `README`）
- 模块：`github.com/quic-go/quic-go`，`go 1.23`；测试大量使用 Ginkgo v2 / Gomega / gomock / testify
- 范围：仓库内 215 个 `*_test.go`。先按 `stress|concurrent|goroutine|pool|alloc|gc|timer|preempt|sched|atomic|chan|select|append|map|slice|shutdown|timeout|cancel|race|leak|stack` 筛选函数名/`It` 描述，再精读约 22 个文件：根包连接/流/传输/定时器/缓冲池、`internal/ackhandler`、`internal/congestion`、`internal/wire`、`internal/utils`、`qlog`、`http3` 与 `integrationtests/self` 的取消/超时/关闭用例；文件:行均相对本仓根目录。
- 说明：根包与 `internal/*` 的单测大多用 gomock 伪造 packet conn / crypto setup，属纯内存逻辑，可提炼为 stdlib 用例；`integrationtests/self` 才是真实 UDP + TLS 1.3 端到端测试。下表把“可移植的机制”与“仅可借鉴协议行为”分开标注。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| buffer_pool_test.go:35 | packet buffer 池化后支持 `Split` 两次得到 3 个共享底层数组的分片，逐个 `Decrement`，越界 Decrement panic | L05/L18 | 低 | `sync.Pool` 存 `[]byte` + refcount；`Split` 返回共享数组子切片，归零后放回；错误容量 Put 触发 panic |
| internal/wire/pool_test.go:9 | StreamFrame 从池中 Get/Put；被外部替换 Data（容量不符）时 Put 必须 panic；:20 验证非池化 frame 走容错分支不 panic | L05/L18 | 低 | 泛型对象池 + 容量校验；验证池化对象的 Data 不被外部引用污染 |
| internal/utils/timer_test.go:40 | 10 次 Reset，交替“读 channel”与“sleep 过窗”，最后一次仍应触发 | L19/L05 | 中 | `time.Timer` 包装器：SetRead 排空 channel、Reset 前 Stop；循环中插入 `runtime.GC()` 验证 timer 对象回收 |
| internal/utils/timer_test.go:100 | 同一已过期 deadline 连续 Reset：中间读过 channel 则应再次触发，未读则不得重复触发 | L19 | 中 | 记录“已读/未读”状态的最小 timer 状态机；用 fake clock 驱动 |
| connection_timer_test.go:12 | idle/ACK/loss/pacing 四种候选 deadline，取最小者作为连接定时器 | L19 | 低 | 结构体四字段 + `time.Timer`，每次 SetTimer 求 min；GC 与频繁 Reset 交叉 |
| internal/congestion/pacer_test.go:11 | 50 包/秒带宽、初始 burst 预算；逐包消耗后按间隔发送，空闲可积累但受 max burst 限制 | L19/L03 | 中 | 令牌桶 + `time.Time` 数学；用固定时钟虚拟推进，避免真实 sleep |
| internal/ackhandler/sent_packet_handler_test.go:515 | PTO：丢包检测定时器触发 probe，ACK 后退避计数复位 | L19/L18 | 高 | 简化发送历史（序号+时间+ack 状态）+ PTO 计数；单 goroutine 时钟推进，校验定时器与重发 |
| internal/ackhandler/sent_packet_handler_test.go:1240 | 5 个 seed 的随机化发送/丢包/ACK/PTO 事件序列，维护不变量 | L19/L18/L02/L03 | 高 | `math/rand/v2.ChaCha8` 可复现随机；事件循环 + 断言（连续 ACK 区间、无重复、定时器单调） |
| internal/ackhandler/received_packet_tracker_test.go:88 | 首包立即 ACK；后续包按 MaxAckDelay 生成 alarm；ACK 出队后 alarm 清零 | L19 | 中 | 收包时间戳 + 延迟 ACK 阈值；用 fake now 验证 GetAlarmTimeout 与 GetAckFrame 状态转换 |
| internal/ackhandler/received_packet_history_test.go:94 | 最多保留 MaxNumAckRanges 个 ACK 区间，超限时淘汰最旧区间 | L13/L19 | 中 | 有序区间 slice/环；插入乱序序号，校验容量上限与淘汰顺序 |
| frame_sorter_test.go:1367 | 达到最大 gap 数后再推入间隔数据返回错误，不得无界增长 | L13/L05 | 中 | 有界 gap map/堆 + 上限计数；失败路径后 GC 验证内存释放 |
| frame_sorter_test.go:1377 | 1000 个帧乱序推送，含重复/重叠，最终必须按 offset 还原完整数据流（6 个子用例） | L13/L05/L17/L03 | 高 | offset 区间堆 + 去重 map + 输出缓冲；每 N 次 Push 强制 GC，校验数据逐字节一致 |
| streams_map_outgoing_test.go:405 | 100 个 goroutine 并发 `OpenStreamSync`，随机逐步放开 stream limit，应按序解除阻塞并发出 STREAMS_BLOCKED | L02/L04 | 高 | 等待队列 + 条件变量/chan；限流值递增广播；校验唤醒顺序与编号 |
| streams_map_outgoing_test.go:456 | 100 个并发 OpenStreamSync 中随机取消 10 个 context，其余按限流放行 | L02/L18 | 高 | 每等待者一个 `chan struct{}` + `sync.Once`；取消时从 map 删除，GC 后确认无残留 |
| streams_map_outgoing_test.go:110 | `CloseWithError` 时把已打开的全部 stream 置为 closed 并传递同一错误 | L18/L02 | 中 | 注册表 close 广播；每 stream 一个 done channel/错误字段，GC 后检查 map 为空 |
| send_stream_test.go:834 | 并发执行 `Write`、`popStreamFrame`、`CancelWrite`，允许竞态但不得死锁/丢唤醒 | L02/L18/L17 | 高 | 发送缓冲 + `sync.Mutex`/条件变量；并发写、取帧、取消；超时保护，GC 插入 |
| send_stream_test.go:985 | 4MB 数据写入，50% 帧确认、50% 帧丢失并重传，最终接收数据必须完整一致 | L05/L18 | 高 | 保留发送窗口切片 + ack watermark；周期性“丢包”触发重传，统计字节与 GC 后存活 |
| receive_stream_test.go:600 | 多 goroutine 并发 Read 同一接收流，数据不重复不丢失 | L02/L17 | 中 | 共享 ring/切片 + 互斥；读偏移原子推进；GC 打断读循环 |
| connection_test.go:898 | 未处理包达到 MaxConnUnprocessedPackets 后，后续包直接丢弃并上报 tracer，不得阻塞 | L05/L19 | 中 | 有界 channel + `select/default`；计数达阈值后丢弃并原子记数 |
| connection_test.go:1511 | 密钥未就绪时先缓存多个包，握手事件到达后按序解包 | L05/L18 | 中 | 包队列 + 状态机；chan 通知握手完成；GC 后检查缓存释放 |
| connection_test.go:1749 | 发送队列阻塞时必须重置 pacing timer，否则 run loop 忙等；迭代次数必须 <3 | L03/L19 | 高 | 事件循环 + 两个 timer；阻塞的 send channel 触发 timer 重置，atomic 统计循环次数 |
| connection_test.go:1951 | ACK alarm 到期驱动第二次发包，两次发送间隔约等于 alarm 时间 | L19 | 高 | 单 goroutine run loop + `time.Timer`；fake send channel，记录两次发送时间差 |
| qlog/writer_test.go:30 | 连续 1000 个 tracer 事件写入限额 writer，写失败后 Close 必须停止并记录错误 | L05/L18 | 中 | bufio writer + error 短路；大量事件下 GC，Close 后检查 writer 状态与错误 |
| qlog/connection_tracer_test.go:704 | PTO 计数变化事件在 Close 时 flush 为单条 JSON | L19/L05 | 低 | 缓冲事件列表 + Close flush；校验事件顺序与资源释放 |
| internal/wire/frame_parser_test.go:151 | 解析 STREAM 帧；`BenchmarkParseStreamAndACK`（:375）反复解析 ACK+STREAM 200B 数据 | L05/L13 | 中 | 复用 `[]byte` 缓冲 + varint 解码；bench 循环中周期性 GC，检查分配次数 |
| internal/wire/stream_frame_test.go:13 | OFF/LEN/FIN 位组合、空 payload、offset/LEN 溢出与超长帧拒绝 | L13/L05 | 中 | 表驱动字节流解析；边界长度与溢出回绕检查 |
| internal/utils/ringbuffer/ringbuffer_test.go:9 | 泛型 RingBuffer 的 Push/Peek/Pop、扩容回绕、Clear | L01/L13/L05 | 中 | `RingBuffer[T]` 双指针+扩容；覆盖空/满/回绕；GC 后复用 |
| config_test.go:136 | 用反射枚举 Config 字段并 Clone，函数字段按引用复制、非函数字段深拷贝、返回副本 | L06/L13 | 中 | `reflect` 遍历字段，指针/切片/map 深拷贝；验证修改副本不影响原值 |
| http3/transport_test.go:339 | 请求 context 取消后连接仍被复用，下一请求成功；只发生一次 Dial | L02/L18 | 高 | mock Dial 函数 + 连接池；取消只影响单请求，GC 后连接对象仍存活 |
| integrationtests/self/cancelation_test.go:507 | 500 条流上每流 4 个 goroutine 随机 Read/Write/Close/CancelRead/CancelWrite，连接必须保持可用 | L02/L04/L18 | 高 | 仅借鉴：用内存 stream 对象 + 随机操作；不可移植点为真实 UDP/TLS |
| integrationtests/self/timeout_test.go:234 | 150ms idle timeout，记录最后 ack-eliciting 包发送/接收时间，连接按时关闭 | L19 | 高 | 仅借鉴：定时器 + 包 tracer；可用回环 UDP 自建报文循环近似 |
| transport_test.go:122 | Transport 与 Listener 并发 Close；Dial 与 Close 并发，错误必须是 ErrTransportClosed 而非 DeadlineExceeded | L02/L19 | 中 | 仅借鉴：`net.ListenUDP` 回环 + 关闭广播；不可直接复刻 QUIC transport |
| sys_conn_oob_test.go:103 | Linux/BSD 上通过 recvmsg 读取 ECN/目的地信息，双栈 IPv4/IPv6 校验 | L15/L19 | 高 | 不可移植：`x/sys/unix`、IP_PKTINFO/IPV6_PKTINFO、平台 socket 选项 |

## 推荐收录（19 条）

1. `l05_packet_buffer_pool` — `buffer_pool_test.go:35`。共享底层数组 + refcount + Split/Decrement；P1，L05/L18。
2. `l05_stream_frame_pool` — `internal/wire/pool_test.go:9`。对象池容量校验与错误 Put；P1，L05/L18。
3. `l19_timer_reset_semantics` — `internal/utils/timer_test.go:100`。同一 deadline 的读/未读状态机；P1，L19。
4. `l19_conn_timer_modes` — `connection_timer_test.go:12`。四类 deadline 取最小值；P1，L19。
5. `l19_pacer_token_bucket` — `internal/congestion/pacer_test.go:11`。虚拟时钟令牌桶与 burst 上限；P1，L19/L03。
6. `l19_pto_recovery` — `internal/ackhandler/sent_packet_handler_test.go:515`。丢包探测定时器 + 退避；P2，L19/L18。
7. `l19_loss_randomized` — `internal/ackhandler/sent_packet_handler_test.go:1240`。可复现随机丢包/ACK 序列；P2，L19/L18/L02。
8. `l19_ack_alarm` — `internal/ackhandler/received_packet_tracker_test.go:88`。延迟 ACK 状态机；P1，L19。
9. `l13_ack_ranges_bounded` — `internal/ackhandler/received_packet_history_test.go:94`。ACK 区间容量上限与最旧淘汰；P1，L13/L19。
10. `l13_frame_sorter_gaps` — `frame_sorter_test.go:1377`。乱序/重复/重叠帧重组 + GC；P2，L13/L05/L17。
11. `l02_streams_map_open` — `streams_map_outgoing_test.go:405`。100 并发 OpenStreamSync + 滑动 limit；P2，L02/L04。
12. `l02_streams_map_cancel` — `streams_map_outgoing_test.go:456`。并发打开 + context 取消；P2，L02/L18。
13. `l02_sendstream_write_cancel` — `send_stream_test.go:834`。并发写/取帧/取消；P2，L02/L18/L17。
14. `l18_sendstream_retransmit` — `send_stream_test.go:985`。ACK 前重传与窗口释放；P2，L05/L18。
15. `l03_pacing_sendqueue` — `connection_test.go:1749`。阻塞发送队列下重置 timer，防忙等；P1，L03/L19。
16. `l19_ack_timer` — `connection_test.go:1951`。ACK alarm 驱动发包；P1，L19。
17. `l05_unprocessed_packet_cap` — `connection_test.go:898`。未处理包上限与丢弃；P1，L05/L19。
18. `l18_qlog_writer_error` — `qlog/writer_test.go:30`。大量事件 + 写失败短路；P1，L05/L18。
19. `l01_ringbuffer_generic` — `internal/utils/ringbuffer/ringbuffer_test.go:9`。泛型环 buffer 回绕/扩容；P1，L01/L13。

## 不可移植清单

- **真实 UDP/TLS/QUIC 端到端**：`integrationtests/self/*`（handshake、timeout、cancelation、zero_rtt、http_shutdown、multiplex、mitm 等）。依赖真实 UDP socket、TLS 1.3 握手、QUIC 报文与证书；只能借鉴场景，不能 stdlib 等价重写。
- **TLS/crypto 内部**：`internal/qtls/cipher_suite_test.go`（起 TCP + `crypto/tls` 服务端、测试证书）；`internal/handshake/crypto_setup_test.go`、`updatable_aead_test.go`、`initial_aead_test.go`、`retry_test.go`（TLS key schedule、0-RTT、AEAD、Retry 完整性标签）。依赖 TLS 协议与 crypto/tls 内部行为。
- **平台 syscall/网卡特性**：`sys_conn_oob_test.go`（`x/sys/unix`、IP_PKTINFO/ECN、recvmsg）、`sys_conn_helper_linux_test.go`、`sys_conn_windows_test.go`、`quic_suite_linux_test.go`（读内核版本）、`transport_test.go:122`/`server_test.go` 中使用 `newUPDConnLocalhost` 与 UDP GSO 的用例。依赖 Linux/Windows/darwin 平台 socket API 与内核能力。
- **HTTP/3 与 QPACK**：`http3/**`。依赖 `quic-go/qpack`、HTTP/3 帧与控制流语义；`transport_test.go:339` 虽用 mock 可借鉴，但完整场景不可 stdlib 化。
- **文件系统/qlog 落盘**：`qlog/qlog_dir_test.go`。依赖 `QLOGDIR`、文件创建与目录权限。
- **互操作/模糊测试**：`interop/**`（Docker 网络与多实现互通）、`fuzzing/**`（语料与 fuzz 引擎）。环境依赖强且不是运行级稳定性用例。
- **生成的 mock**：`mock_*_test.go`。由 mockgen 生成，只服务于单测注入；场景应重写为手写接口假实现。
- **构建标签/平台特化**：`quic_suite_linux_test.go`、`integrationtests/self/self_suite_linux_test.go` 等使用 `//go:build linux/windows/darwin`；原样搬运会破坏多平台矩阵，只能作为 L15 的“平台差异”参考。
- **真实证书/testdata**：`internal/testdata` 证书、`integrationtests/self` 的 TLS 配置。证书校验与握手时序无法用纯 stdlib 逻辑复刻。
- **基线依赖**：Ginkgo/Gomega/gomock/testify 仅用于组织测试；场景重写时用手写断言，避免测试框架差异掩盖编译器问题。

## 备注

- 最适合直接落地的是 L19：`internal/utils/timer`、`connection_timer`、pacer、ACK alarm、PTO 五组测试都可用 fake clock 在纯 stdlib 下稳定复现。
- L05/L18 的核心是“池化对象 + 引用计数 + 错误路径不归还”；`buffer_pool_test.go` 与 `internal/wire/pool_test.go` 给出了很好的 panic/容量契约。
- L02/L04 的高价值场景在 `streams_map_outgoing_test.go` 与 `send_stream_test.go`：大量 goroutine 等待同一限流条件、context 取消竞态、并发写与取消；用 channel + 条件变量即可重写。
- L17 在本库没有显式断言，但 frame sorter（byte slice 堆）与 send stream（缓冲切片 + 锁）适合作为“栈/寄存器指针位图 + GC 打断”的载体。
