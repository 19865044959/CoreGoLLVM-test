# pion/webrtc v4.1.2

- 来源：`research/repos/webrtc`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / 必要测试辅助源码）
- 模块：`github.com/pion/webrtc/v4`，`go 1.20`；测试基于 `testify`、`pion/logging`、`pion/transport`、`pion/interceptor`，根包用例普遍通过 `newPair()` 建真实 localhost UDP + ICE + DTLS + SCTP 连接。
- 本地路径：`research/repos/webrtc`；HEAD `f5d98cea`，`git describe --tags` = `v4.1.2`。
- 范围：仓库内 78 个 `*_test.go`。按 `concurrent|goroutine|checkroutines|close|graceful|timeout|deadline|buffered|ordered|detach|flow|pool|reflect|interface|codec|state` 过滤后精读 17 个文件：`operations_test.go`、`datachannel*_test.go`、`peerconnection_close_test.go`、`peerconnection_go_test.go`、`sctptransport_test.go`、`peerconnection_renegotiation_test.go`、`mediaengine_test.go`、`api_test.go`、`internal/mux`、`internal/util`、`pkg/null`、`pkg/media/{samplebuilder,rtpdump,h264reader}`。文件:行均相对本仓根目录。
- 说明：`pkg/media`、`internal/mux`、`internal/util`、`pkg/null`、`operations`、`MediaEngine`、`API` 选项为纯内存/纯解析逻辑，可直接改写；DataChannel/PeerConnection 用例的“真实网络”部分不可移植，但其队列、引用计数、回调与关闭竞态场景可用内存管道 + fake transport 复现。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| operations_test.go:13 | 100 轮 × 16 个 operation 入队，`Done` 后按 FIFO 全部执行；第 50 轮后置位 negotiation-needed 标志 | L02/L04 | 中 | `sync.Mutex` 保护切片队列 + 单 worker；每轮回填 0..15 的平方并断言顺序；标志用 `atomic.Bool` |
| operations_test.go:55 | `GracefulClose` 后不再执行任何新入队 operation，关闭前的计数保持不变 | L02/L19 | 中 | 队列 + closed 原子标志；关闭后 Enqueue 直接丢弃；用计数与 WaitGroup 验证 |
| internal/mux/mux_test.go:65 | 底层连接返回 timeout/短缓冲时 readLoop 不得退出；只有 EOF 才结束 | L19/L04 | 中 | 自定义 `io.Reader` 按序返回 `os.ErrDeadlineExceeded`/`io.ErrShortBuffer`/数据/EOF；断言前两次读到数据、最后循环退出 |
| internal/mux/mux_test.go:111 | endpoint 缓冲满（ErrFull）是非致命错误，mux 不能被销毁，后续包仍可分发 | L02/L19 | 中 | 有界 endpoint 队列 + `select/default` 丢弃计数；压力写 25 个包后仍能 Close |
| internal/mux/mux_test.go:160 | 未知协议的 pending 包有上限（maxPendingPackets），空包不入队 | L13/L05 | 低 | 环形/切片队列 + 容量上限；先塞满再分发，断言长度等于上限 |
| internal/util/util_test.go:18 | 扁平化多错误、去 nil，`Error()` 换行拼接，`Is` 命中子错误 | L06/L05 | 低 | 自定义 `multiError []error` 实现 `error`/`Is`；构造嵌套切片并用 `errors.Is` 校验 |
| pkg/media/samplebuilder/samplebuilder_test.go:354 | 首个 gap 后按 maxLate 丢弃旧包，后续样本记录 `PrevDroppedPackets` | L13 | 中 | 以 uint16 序列号为索引的槽位数组；按 maxLate 推进读指针；校验丢弃计数 |
| pkg/media/samplebuilder/samplebuilder_test.go:523 | 连续 Push 0x20000 个 RTP 包并即时 Pop，序号回绕下样本时间戳/长度/内容逐字节正确，仅最后 1 包滞留 | L03/L05/L13 | 中 | 长循环 + 定长槽位复用；每 N 次插 `runtime.GC()` 与调度点；校验 0x1FFFF 个样本 |
| pkg/media/samplebuilder/samplebuilder_test.go:553 | 每个包设 finalizer，Push/Pop 0x20000 次后 `runtime.GC()`，确认除最后一包外全部被解除引用 | L18/L05 | 高 | `runtime.SetFinalizer` + 原子引用计数 + `runtime.KeepAlive`；GC 后断言存活数为 1 |
| pkg/media/samplebuilder/samplebuilder_test.go:411 | 序列号从 0xFFF8/0xFFFE 起始时旧槽位仍被正确清理，跳变后新包保留 | L13 | 低 | 取模 65536 的槽位索引；表驱动覆盖回绕边界 |
| pkg/media/samplebuilder/sampleSequenceLocation_test.go:12 | uint16 区间比较（before/inside/after）跨 0 回绕 | L13 | 低 | 无符号差值比较；表驱动覆盖 0xffff→0x0000 |
| pkg/media/rtpdump/reader_test.go:17 | 流式解析 rtpdump：magic/preamble、头部、包长度、截断/畸形输入逐项报错 | L13/L05 | 中 | `bufio.Reader` + 显式长度字段；表驱动畸形输入；限制单包最大长度防放大 |
| pkg/media/rtpdump/rtpdump_test.go:70 | Header/Packet 二进制 marshal→unmarshal 往返一致 | L13 | 低 | 固定宽度大端编码 + `bytes.Buffer`；覆盖 offset/RTCP 标志 |
| pkg/media/h264reader/h264reader_test.go:49 | 解析 Annex-B 起始码与 NAL 头（forbidden bit/ref idc/type/POC） | L13/L05 | 中 | 逐字节扫描起始码 + 位域解包；表驱动头部组合 |
| pkg/media/h264reader/h264reader_test.go:65 | 只有起始码、无负载或空输入时 `NextNAL` 返回 EOF 而非 panic | L13 | 低 | 边界长度检查；覆盖 0/3/4 字节输入 |
| pkg/null/null_test.go:12 | 各基础类型的 nullable 包装器构造与 Valid 语义 | L06/L01 | 低 | 泛型 `Nullable[T]` 或每类型结构体；批量构造并断言 |
| mediaengine_test.go:577 | codec 注册表：同 codec 重复注册幂等、大小写不敏感、同 payload 不同 codec 报错、RTX/FEC 识别 | L06/L13 | 中 | `map[payloadType]Codec` + 规范化 MIME key；接口 `Codec` 提供 Name/MimeType；覆盖冲突 |
| mediaengine_test.go:950 | 多 codec 协商：按远端 SDP 顺序匹配本地能力并生成统一 payload 集合 | L06 | 中 | codec 接口切片 + 稳定排序/去重；构造 fake SDP 参数表驱动 |
| api_test.go:15 | functional options：`WithSettingEngine`/`WithMediaEngine` 注入，nil 时回退默认值 | L06/L12 | 低 | `type Option func(*Config)`；依次应用并断言默认字段非 nil |
| datachannel_go_test.go:68 | 512 条消息单 channel 保序；处理函数在流中途被替换仍不丢消息 | L02/L04 | 高 | 单消费者队列 + 回调原子替换；生产 goroutine 随机 sleep；消费端校验 1..512 |
| datachannel_go_test.go:180 | BufferedAmountLow 阈值回调：open 前设置暂存、open 后生效，双向计数用原子变量 | L02/L19 | 高 | 发送缓冲计数 + 阈值比较 + 回调注册表；fake 发送器逐步排空触发回调 |
| datachannel_go_test.go:635 | Detach 后从 SCTP transport 的 dataChannels 注册表移除，避免悬挂引用 | L18/L13 | 中 | 注册表 map/slice + Detach 删除；GC 后确认无引用 |
| peerconnection_close_test.go:235 | 50 个 goroutine 并发 `GracefulClose`，混合 `Close`，全部返回且不重复关闭 | L02/L04/L19 | 高 | `sync.Once` + 关闭状态机 + WaitGroup；50 并发调用，超时保护，GC 后检查协程退出 |
| peerconnection_go_test.go:1586 | 在 ICE 状态回调里调用 `Close`，不得死锁；关闭后回调不再触发 | L02/L04/L19 | 高 | 状态回调在独立 goroutine 执行 + 回调注销；Close 内等待 worker 退出但避免反向加锁 |
| sctptransport_test.go:21 | 按 DTLS role（client 偶数/server 奇数）分配最小未用 data channel ID，并登记 used 表 | L06/L13 | 低 | 角色决定步长 2 + 起始 0/1；扫描 used map；表驱动覆盖空洞与已用集合 |
| sctptransport_test.go:124 | 10 个 goroutine 并发建立 ORTC detach 会话，双向写入并 Detach，全部成功 | L02/L04 | 高 | 内存 stream + 每会话独立连接对象；并发建/读/写/detach，超时保护 |
| peerconnection_renegotiation_test.go:970 | 单侧连续加 500 条 track，每次 negotiation-needed 触发重协商，最终对端 transceiver 数一致 | L02/L03/L04 | 高 | fake signal 通道 + 事件循环 + 500 次状态推进；每轮插 GC 与调度点 |
| datachannel_test.go:75 | 2/4/8/16/32 个并发 sender 的吞吐基准 | L02/L03 | 中 | `b.RunParallel` 或 N 个 producer + 单 consumer；统计发送计数 |

## 推荐收录（15 条）

1. `l02_operations_fifo` — `operations_test.go:13`。16 个 operation × 100 轮 FIFO 执行与 negotiation 标志；P1，L02/L04。
2. `l02_operations_graceful_close` — `operations_test.go:55`。关闭后拒绝新任务、不执行残留；P1，L02/L19。
3. `l19_mux_nonfatal_read` — `internal/mux/mux_test.go:65`。timeout/短缓冲不终止读循环，EOF 才终止；P1，L19/L04。
4. `l02_mux_dispatch_backpressure` — `internal/mux/mux_test.go:111`。endpoint 队列满非致命、mux 存活；P1，L02/L19。
5. `l03_samplebuilder_long_loop` — `pkg/media/samplebuilder/samplebuilder_test.go:523`。0x20000 包长循环 + 序号回绕；P2，L03/L05/L13。
6. `l18_samplebuilder_finalizer` — `pkg/media/samplebuilder/samplebuilder_test.go:553`。finalizer + GC 验证 packet 全部解除引用；P2，L18/L05。
7. `l13_rtpdump_stream_parse` — `pkg/media/rtpdump/reader_test.go:17`。流式二进制解析 + 畸形输入；P1，L13/L05。
8. `l06_mediaengine_codec_registry` — `mediaengine_test.go:577`。codec 注册/冲突/大小写/RTX/FEC；P1，L06/L13。
9. `l06_api_functional_options` — `api_test.go:15`。函数选项与 nil 默认化；P0，L06/L12。
10. `l02_datachannel_ordered` — `datachannel_go_test.go:68`。512 条消息保序 + 回调热替换；P2，L02/L04。
11. `l02_datachannel_buffered_low` — `datachannel_go_test.go:180`。BufferedAmountLow 阈值与双向原子计数；P2，L02/L19。
12. `l18_datachannel_detach_registry` — `datachannel_go_test.go:635`。Detach 从注册表移除、GC 无悬挂；P1，L18/L13。
13. `l02_peerconnection_close_concurrent` — `peerconnection_close_test.go:235`。50 并发 GracefulClose/Close 不重复关闭；P2，L02/L04/L19。
14. `l06_sctp_datachannel_id` — `sctptransport_test.go:21`。按 role 分配 channel ID 与 used 表；P1，L06/L13。
15. `l02_renegotiation_stress` — `peerconnection_renegotiation_test.go:970`。500 track 单侧重协商压力；P2，L02/L03/L04。

## 不可移植清单

| 测试文件:行 | 依赖 | 不可移植原因 |
|---|---|---|
| e2e/e2e_test.go:43 | 真实 UDP + ICE + DTLS + SRTP 媒体管线 | 需要完整 WebRTC 协议栈与真实 socket，无法在单包 stdlib 用例中复现 |
| e2e/e2e_test.go:154 | 真实 PeerConnection 数据通道 | 依赖 `newPair` 的 UDP/DTLS/SCTP 握手 |
| peerconnection_test.go:120 | `NewPeerConnection` 创建 ICE agent / UDP mux | 构造即触碰网络与 goroutine；只能借用状态机片段 |
| dtlstransport_test.go:19 | 真实 DTLS 握手、自签证书、fingerprint | 依赖 DTLS 协议与 crypto/tls 证书链 |
| icetransport_test.go:19 | 真实 ICE connectivity check / STUN | 需要 socket、候选对与超时重传 |
| icegatherer_test.go:20 | 真实候选收集（host/STUN/mDNS） | 依赖网卡枚举、mDNS、网络环境 |
| sctptransport_test.go:65 | 完整 PeerConnection + SCTP association | `newPair` 端到端；关闭回调依赖 DTLS 链路 |
| sctptransport_test.go:124 | 10 组真实 ORTC 会话 | 每组都含 UDP/DTLS/SCTP，耦合外部网络 |
| datachannel_go_test.go:68 | `newPair` + SCTP 有序通道 | 测试本体不可移植；场景可用内存队列重写（见推荐 10） |
| datachannel_go_test.go:180 | 对端真实接收与缓冲排空 | 依赖 SCTP 发送缓冲与 wire 往返 |
| peerconnection_close_test.go:235 | 真实 PeerConnection 生命周期 | 关闭路径含 ICE/DTLS/SCTP worker；场景可借鉴，测试不可直接搬 |
| peerconnection_go_test.go:1586 | 真实 ICE 状态回调 | 需要真实连接失败/关闭事件驱动 |
| peerconnection_renegotiation_test.go:970 | 真实 SDP 协商 + 500 track | 依赖完整信令与媒体链路 |
| datachannel_test.go:75 | `newPair` 基准 | 吞吐量受真实 UDP/SCTP 影响 |
| peerconnection_media_test.go:49 | SRTP/RTP/RTCP 媒体收发 | 依赖 SRTP 密钥与真实网络抖动 |
