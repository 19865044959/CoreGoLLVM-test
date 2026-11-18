# fasthttp v1.59.0

- 来源：`research/repos/fasthttp`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / `README`，无实现源码）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/fasthttp`
- 版本核验：`git describe` = `v1.59.0`，HEAD `bb94b26b`（2025-02-14，`add dummy support for js,wasm (#1955)`）；`go.mod` 声明 `go 1.21`
- 重点维度：L05 大量分配、L07 unsafe/零拷贝、L11 编译期优化、L02 高并发、L06 接口、L19 长连接/超时/关闭、L18 池化复用
- 测试规模：61 个 `*_test.go`，本次精读约 24 个（`server_test.go`、`http_test.go`、`header_test.go`、`args_test.go`、`uri_test.go`、`client_test.go`、`userdata_test.go`、`allocation_test.go`、`fasthttputil/*`、`stackless/*` 等）
- 依赖提示：`go.mod` 依赖 `andybalholm/brotli`、`klauspost/compress`、`valyala/bytebufferpool`、`golang.org/x/{crypto,net,sys}`；这些实现全部不在 partial clone 内，用例一律用 stdlib 重写
- 构建标签：`allocation_test.go` 为 `!race`；`round2_64_test.go` / `bytesconv_64_test.go` 限 64 位（`amd64||arm64||ppc64||ppc64le||riscv64||s390x`），`*_32_test.go` 对应 32 位；`tcplisten`、`prefork`、`reuseport` 属平台/进程特化

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| `server_test.go:38` | POST 后多一个 CRLF 的 pipelined 请求，仍按序响应，多余 CRLF 不吞下一请求 | L02/L07 | 中 | `bufio.Reader` 手写请求循环，保留未消费字节；两次响应之间 `runtime.GC()` |
| `server_test.go:126` | 第二个请求只发了半包，第一个响应必须立即 flush，不得等第二个请求补齐 | L02/L19 | 中 | 两个 `net.Pipe` 端；写端延迟发送后半包，读端用带超时的读断言先拿到第一个响应 |
| `server_test.go:249` | 同一 keep-alive 连接两个请求，连接状态序列必须是 new→active→idle→active→idle→closed | L02/L19 | 低 | 内存 listener + 状态回调；状态写入 chan 串行化，关闭后校验完整序列 |
| `server_test.go:461` | 20B 读缓冲下超长 header 返回 431 + `Connection: close`，handler 不执行 | L05/L07 | 低 | 固定 20B 读缓冲解析请求行/头，返回 431；GC 后复用缓冲再跑一轮 |
| `server_test.go:676` | 响应流式写出：handler 先 flush 第一段，阻塞到客户端读到后再写第二段 | L02/L19 | 中 | `SetBodyStreamWriter` 等价物：`bufio.Writer` 分块写 + `Flush`，`chan` 控制节奏，校验 chunk framing |
| `server_test.go:767` | `DisableKeepalive`：响应带 `Connection: close`，随后读到 EOF | L02/L19 | 低 | 响应头写 close，处理循环退出并关闭连接；关闭后活跃连接计数为 0 |
| `server_test.go:840` | 每 IP 连接数上限 1：第二条连接立即 429，第一条正常服务 | L02/L19 | 中 | 连接注册表 + `map[ip]*atomic.Int32` 计数；超限直接写 429 并关闭；释放后计数回落 |
| `server_test.go:944` | 并发处理数上限 1：第二条连接立即 503，第一条仍成功 | L02/L04 | 中 | 带缓冲信号量（chan）限流；`select default` 拒绝；handler 释放令牌后可继续接受 |
| `server_test.go:1491` / `server_test.go:1522` | 短生命周期读写缓冲：串行/10 goroutine 各 10 个 keep-alive 请求，验证低内存模式无泄漏 | L05/L18 | 中 | 每次连接新建/池化 `bufio.Reader`+`Writer`，请求间 `runtime.GC()`，校验无跨连接残留 |
| `server_test.go:2264` | ctx 归还池前 user value 的 `io.Closer` 仍能读取完整 Request/Response | L05/L18/L07 | 高 | 自建 ctx 池；Close 回调在 reset 前执行；reset 后再 GC，验证无法再观察到旧数据 |
| `server_test.go:2368` | hijack 后 user value 必须 reset 并触发其 `io.Closer`，不得留在 ctx 上 | L05/L18 | 高 | hijack 标记 + 清理钩子；GC+finalizer 确认 user value 可回收，连接计数归零 |
| `server_test.go:2839` | `ctx.Init` 绑定 `Logger` 接口，connID 全局递增格式化日志 | L06/L02 | 低 | 自定义 `Logger` 接口实现 + 原子 connID；并发 Init 校验日志唯一且格式确定 |
| `server_test.go:2858` / `2914` / `2981` | 超时处理器：正常、超时返回 408、超时后同一连接可继续处理下一请求 | L02/L19/L18 | 高 | worker goroutine + `select`/`time.After`；超时后原 handler 继续跑，完成时不得写已复用 ctx |
| `server_test.go:3552` | Shutdown：停止 accept，等待在途 500ms 请求完成后 Serve 返回，新连接失败 | L02/L19 | 高 | 内存 listener + 连接 registry；关闭监听、等待 WaitGroup、拒绝新连接；并发收尾 |
| `server_test.go:3797` / `3838` | Shutdown 立即关闭空闲连接；带 context 超时时返回 `DeadlineExceeded`，活跃连接保留 | L19/L02 | 中 | 空闲集合与活跃集合分离；`context.WithTimeout` + 原子 open 计数；超时后状态可判定 |
| `server_test.go:3935` | 按请求头回调下发更小的 body 上限，超限返回 `ErrBodyTooLarge` | L05/L19 | 低 | 头部解析后计算 per-request limit，超限立即停止读并关闭 |
| `server_test.go:3960` | 请求体流式读：先给 32KB，handler 读完后才写入下一段 64KB | L02/L05/L19 | 高 | `io.Pipe` 模拟分段上传；handler 阻塞读 + 二次写入；max body 限制；段间 GC |
| `server_test.go:4115` / `4165` | per-request 读/写超时：慢速 body 与无限流式响应必须在 1ms 后超时 | L19/L02 | 高 | `SetReadDeadline`/`SetWriteDeadline` + 分片慢写端；断言 timeout 错误且无 goroutine 泄漏 |
| `server_test.go:4248` | chunked 响应 + trailer：三段 body 逐段 flush，终止块与 trailer 正确 | L05/L19 | 中 | `bufio.Writer` + chunk 编码器；trailer 排序后输出保证确定性；大响应分块、客户端逐块读；GC 插入在块之间 |
| `http_test.go:117` / `138` | Request/Response `CopyTo` 后与源对象 `reflect.DeepEqual`（含 header/body/内部字段） | L07/L05/L18 | 中 | 结构体含 raw body/header 子切片；CopyTo 显式复制底层数组；目标与源独立，源 reset 后目标仍可读 |
| `http_test.go:456` / `510` | 10 goroutine 并发 `SwapBody` 20 轮，交换 body 缓冲所有权；流式 body 交换后旧缓冲不得被复用 | L02/L05/L07 | 中 | 每 worker 独立 Acquire/Release，交换 `[]byte` 所有权；每轮 GC，校验旧 body 内容不被池化对象改写 |
| `http_test.go:838` | `ReleaseRequest` 必须清空 `UseHostHeader` 等标量状态，下次 Acquire 不复现旧值 | L05/L18 | 低 | 池化对象 Reset 清零全部字段；循环 Acquire/Release + GC；字段级断言 |
| `http_test.go:2585` / `2597` | `SetBodyRaw` 让 body 直接引用调用方切片，Reset 后必须断开引用 | L07/L18 | 中 | 记录引用缓冲；Reset 置 nil；GC/finalizer 验证原切片可被回收 |
| `http_test.go:3146` | 1000 次 `SetBodyRaw` + `CopyTo`，每个副本 body 必须独立且保持原值 | L07/L05/L18 | 中 | 池化对象数组；raw body 复制策略；最后 GC 后逐个校验、再统一 Release |
| `header_test.go:325` | `RawHeaders()` 返回原始 header 块（保留大小写、空格、CRLF），与 `Peek` 规范化视图并存 | L07/L05 | 中 | 手写 header 扫描器，raw 块与 kv 索引共享同一缓冲区；校验 raw 视图逐字节一致 |
| `header_test.go:116` | 多行（obs-fold）header 与制表符空白裁剪，request/response 两条路径一致 | L07/L11 | 低 | 逐字节状态机处理 `\t`、续行、首尾空白；表驱动断言 |
| `header_test.go:871` | 超长 header 的 debug snippet 截断：≤200B 全量，更长保留头尾各 200B | L05/L11 | 低 | 固定规则截断函数，覆盖 199/200/400B 边界；GC 后重复调用 |
| `header_test.go:1125` | header 参数（`;key=value`）遍历：引号、转义、空值、非法输入、回调提前终止 | L06/L07 | 低 | 回调式参数扫描器；引号内 `\"` 处理；返回 false 提前停止 |
| `header_test.go:2368` / `2415` | 10 万 header 超限报错；每次只返回 1 字节的 reader 也能增量解析 | L05/L11 | 中 | 4KB `bufio.Reader` + 限长；1B/次 reader 驱动状态机；错误路径 GC |
| `args_test.go:108` / `483` / `150` | 池化 Args 10 goroutine 并发 Set→Reset→Parse；重复 key、空值、编码值；PeekMulti 返回共享切片 | L02/L07/L05 | 中 | 复用 kv 切片，值别名输入字符串；并发 Acquire/Release；每轮 GC；边界表驱动 |
| `uri_test.go:34` / `468` | 池化 URI 并发解析；`Parse` 不得原地改写调用方输入 buffer | L02/L07/L05 | 中 | 手写百分号解码到独立输出；输入只读断言；10 goroutine 池复用 + GC |
| `uri_test.go:107` / `349` | URI 相对路径合并、query/fragment 拆分、IPv6/UTF-8 host、非法 URL 拒绝 | L07/L11 | 中 | 表驱动解析器，覆盖 `//host`、`?`/`#` 在 path、`%e4%b8%96` 解码；错误路径 |
| `cookie_test.go:288` | Cookie 池 10 goroutine 并发 String→Reset→Parse，字段完整 | L02/L05 | 低 | 复用 cookie 结构 + kv 切片；并发循环 + GC |
| `userdata_test.go:124` / `47` | user data Reset 后值可被 GC（finalizer 触发）；实现 `io.Closer` 的值在 Reset 时被 Close | L18/L06 | 中 | `map[string]any` + Reset 遍历 Close；`runtime.SetFinalizer` 验证回收；保留一个 key 作存活对照 |
| `allocation_test.go:10` | `ServeConn` 处理 100 个请求 0 分配（`testing.AllocsPerRun`） | L05/L11 | 中 | 预分配读写缓冲 + 完全池化；`AllocsPerRun` 断言 0；GC 不进入计数循环 |
| `allocation_test.go:71` | URI Parse 100 次 0 分配 | L05/L11 | 低 | 复用 URI 与解码缓冲；`AllocsPerRun` 断言；失败路径不计入 |
| `workerpool_test.go:18` / `56` | 有界 worker pool 10 次 Start/Stop；WorkerFunc 阻塞时并发数不超上限 | L02/L04 | 中 | 令牌 chan + worker goroutine；原子统计峰值；Stop 后全部退出 |
| `fasthttputil/pipeconns_test.go:123` / `222` | 并发读、写与 Close 交错；关闭后错误只能是约定错误；读/写 deadline 反复触发 | L02/L19 | 中 | `net.Pipe` 包装 deadline；读/写/关闭三 goroutine + 超时保护；错误归一化 |
| `stackless/func_test.go:10` / `stackless/writer_test.go:21` | `any` 参数 + 类型断言调用 4096 次；10 goroutine 各自复用压缩 writer | L06/L02/L05 | 低 | `any` 回调 + 明确断言；每 goroutine 独立 writer，Reset 复用；GC |
| `client_test.go:26` | `CloseIdleConnections` 清空空闲连接，之后池计数为 0 | L02/L19 | 低 | 手写连接池 map + 空闲列表；关闭后计数断言 |
| `client_test.go:1288` / `1384` | 10 并发请求期间 pending 计数恰为 10；`MaxConns=1` 时按 deadline 排队，body 不丢 | L02/L19 | 高 | 连接池 + 等待队列（chan）；原子 pending；deadline 到期返回 `ErrNoFreeConns` |
| `client_test.go:2827` / `2906` | 连接等待超时：成功路径等待队列清空；超时路径返回错误且队列无残留 | L02/L19/L18 | 高 | 带 deadline 的等待队列；释放时唤醒；测试后 GC 检查无泄漏 |
| `fasthttpadaptor/adaptor_test.go:16` / `196` | 自定义 RequestCtx → `net/http.Request` 适配；响应写回；hijack 经接口断言 | L06 | 中 | 自定义 ctx + `http.Handler` 接口；`http.Header`/`url.URL` 转换；hijack 用可选接口断言 |
| `round2_64_test.go:10` / `bytesconv_64_test.go:18` | 切片容量向上取整到 2 的幂（含 MaxInt32/MaxInt64-1 边界）；uint 十进制格式化 | L11/L13 | 低 | 位运算 round-up + 溢出边界；`strconv.AppendUint` 对照 |

## 推荐收录用例（15 条）

1. `l05_serveconn_zero_alloc`（`allocation_test.go:10`，P1）— L05/L11。固定 GET 请求 + 预分配 `bufio` 读写缓冲，循环 100 次解析/生成响应，用 `testing.AllocsPerRun` 断言 0 分配；每轮前 `runtime.GC()` 放在计数外。重写时自写最小的 header 解析与响应编码，避免 `net/http` 的分配。
2. `l07_rawheaders_alias_reuse`（`header_test.go:325`，P1）— L07/L05/L18。手写 header 扫描器，`RawHeaders()`、`PeekAll()`、`PeekKeys()` 都返回底层缓冲的子切片；解析后 `runtime.GC()`，再通过返回值读取；随后 Reset 并复用缓冲，验证旧切片不被新请求内容污染（或明确文档化 aliasing 生命周期）。这是 fasthttp 零拷贝语义的核心。
3. `l07_body_swap_pool_concurrent`（`http_test.go:456`/`510`，P1）— L02/L05/L07/L18。10 个 worker 各自 Acquire/Release 请求与响应，每轮 `SwapBody` 交换 `[]byte` 所有权并 Append；每 5 轮 GC；断言交换回来的旧 body 内容正确、池化对象没有持有旧 buffer。可再叠加 `SetBodyRaw`（`http_test.go:2597`）验证 Reset 断开引用。
4. `l07_uri_parse_no_overwrite`（`uri_test.go:468` + `uri_test.go:34`，P1）— L02/L07/L05。解析含 `%AA` 的 URL 后逐字节断言输入 `[]byte` 未变；10 goroutine 复用池化 URI 反复 Parse；中间插入 GC。重写重点是百分号解码写独立输出，而非原地改输入。
5. `l05_args_pool_inplace_parse`（`args_test.go:108`/`483`/`150`，P1）— L02/L07/L05。池化 kv 切片，表驱动覆盖重复 key、空值、`+`/`%20`、非法百分号；`PeekMulti` 返回共享子切片；10 goroutine Set→Reset→Parse；每轮 GC。
6. `l18_userdata_finalizer_reset`（`userdata_test.go:124`/`47`，P1）— L18/L06。`map[string]any` 保存带 finalizer 的值，Reset 触发 `io.Closer` 并释放引用，GC 后 finalizer 必须执行；保留一个 key 作存活对照，排除“整体被回收”的假阳性。
7. `l05_ctx_pool_reset_uaf`（`server_test.go:2264`/`2368` + `http_test.go:838`，P2）— L05/L18/L07。自建 ctx 池 + user value 清理顺序：Close 回调在 reset 前能读到完整 Request/Response；reset 后旧值不可达；hijack 后连接不入池。用 finalizer 与多次 GC 观察悬垂引用。
8. `l02_conn_state_limits`（`server_test.go:249`/`840`/`944`/`1019`，P1）— L02/L19。内存 listener 驱动：两请求 keep-alive 的状态序列、每 IP 上限 429、并发上限 503、拒绝计数。用 chan 串行化状态，避免 map 遍历顺序；关闭后计数全部归零。
9. `l02_graceful_shutdown`（`server_test.go:3552`/`3613`/`3797`/`3838`，P1）— L02/L19/L18。Shutdown 停 accept、等 500ms 在途 handler、关闭空闲连接；带 context 超时返回 `DeadlineExceeded` 且活跃连接保留；断言 open 计数。全部用内存连接，不依赖真实网络。
10. `l19_timeout_handler_reuse`（`server_test.go:2914`/`2981`，P1）— L02/L19/L18。handler 阻塞，`select` + `time.After` 返回 408；原 handler 稍后完成，不得写已复用 ctx；同一连接下一次请求必须成功。这是池化 ctx 与超时竞争的最典型 UAF 场景。
11. `l19_keepalive_pipeline_flush`（`server_test.go:126`/`767`/`3180`，P1）— L02/L19/L07。第一个请求完整、第二个半包时，第一个响应必须立刻 flush；keep-alive 计数/`Connection: close`/`MaxRequestsPerConn` 三种终止条件；读端带超时判定，不依赖真实等待时长。
12. `l05_body_stream_backpressure`（`server_test.go:3960`/`676`/`4371`，P2）— L02/L05/L19。`io.Pipe` 分两段上传，handler 读第一段成功后写端才发第二段；响应侧 `bufio.Writer` 分块 flush；限制 max body；段间 GC。可加 `server_test.go:4115`/`4165` 的 per-request deadline 变体。
13. `l02_conn_pool_wait_deadline`（`client_test.go:1288`/`1384`/`2827`/`2906`，P2）— L02/L19。手写连接池：最大 1 连接、10 并发、等待队列带 deadline；成功路径 pending 归零、超时路径返回 `ErrNoFreeConns` 且队列无残留；`CloseIdleConnections` 后空闲为 0。
14. `l02_pipe_close_race`（`fasthttputil/pipeconns_test.go:123`/`222`，P1）— L02/L19。`net.Pipe` 包装 deadline，读/写/Close 三 goroutine 交错；只允许约定错误；超时保护；重复 10 轮，GC 后检查 goroutine 数回落。
15. `l06_adaptor_interface`（`fasthttpadaptor/adaptor_test.go:16`/`196`，P1）— L06。自定义 ctx 转 `net/http.Request`，经 `http.Handler` 接口调用，再写回自定义响应；hijack 用可选接口断言。纯 stdlib（`net/http`、`net/url`），覆盖接口方法集、type assertion、适配层零拷贝取舍。

## 不可移植清单

| 位置 | 原因 |
|---|---|
| `server_test.go:1115` / `1165` / `1217` | 真实 TLS 握手；依赖 `GenerateTestCertificate` 生成的自签证书与 `crypto/tls` 状态机。可用 stdlib 重造证书，但成本高、收益偏 L15，不建议纳入 L02/L05/L07 批次 |
| `client_test.go:2379` / `2395` / `2759` | HTTPS + 真实 TCP 监听，TLS 握手超时/证书校验语义 |
| `brotli_test.go` / `zstd_test.go` | 依赖第三方 `andybalholm/brotli`、`klauspost/compress/zstd`，stdlib 无对应实现 |
| `fasthttpproxy/dialer_test.go:16` | HTTP CONNECT/SOCKS 代理 + 真实 TCP 端口 + `golang.org/x/net/http/httpproxy` |
| `prefork/prefork_test.go:27` | `fork/exec`、环境变量传递、fd 继承、子进程信号，属进程模型/平台行为 |
| `tcplisten/tcplisten_test.go` | 构建标签限定 unix；`SO_REUSEPORT`/`TCP_DEFER_ACCEPT` 等内核 socket 选项 |
| `reuseport/reuseport_test.go:9` | 绑定固定端口 10081/10082，依赖 `SO_REUSEPORT` 与 IPv6 回环 |
| `fs_test.go` / `fs_fs_test.go` | 文件系统、`sendfile`、`os.File`；与 GC/UAF 无关，且需要真实文件 |
| `server_test.go:322` | multipart 临时文件落盘，依赖 `os.TempDir` 与清理时序 |
| `client_test.go:2467` / `2520` | 测试体直接使用真实 TCP/unix socket 的 echo server；可改写为内存 conn，原样不可移植 |
| `allocation_test.go:37` | 0 分配客户端测试监听真实 `tcp4`；可保留断言但需替换为 fake `net.Conn` |

## 核验记录

- grep 关键词：`concurrent|goroutine|pool|alloc|unsafe|noescape|string|bytes|reflect|interface|bind|reuse|close|timeout|cancel|race|append|map|slice|chan|select|shutdown|graceful|stream|swap|raw`
- 精读测试文件 24 个（含部分精读）：`server_test.go`、`http_test.go`、`header_test.go`、`args_test.go`、`uri_test.go`、`client_test.go`、`cookie_test.go`、`userdata_test.go`、`allocation_test.go`、`workerpool_test.go`、`peripconn_test.go`、`round2_64_test.go`、`stream_test.go`、`streaming_test.go`、`compress_test.go`、`bytesconv_test.go`、`stackless/{func,writer}_test.go`、`fasthttputil/{pipeconns,inmemory_listener}_test.go`、`fasthttpadaptor/{adaptor,request}_test.go`
- 表中所有 `文件:行` 均经 `sed -n '<line>p'` 核对，行内容为对应 `Test*` 函数声明或关键语句。
