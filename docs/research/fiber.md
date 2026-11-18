# fiber v2.52.6

- 来源：`research/repos/fiber`（partial clone，仅 `*_test.go`、`testdata`、`go.mod`、README 等；无实现源码）
- 本地绝对路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/fiber`
- 模块：`github.com/gofiber/fiber/v2`；版本核验：`git describe --tags --exact-match` = `v2.52.6`，HEAD = `e04f815c43cb8a84f4de90e1d1bd9507bc33fa8c`；`go.mod` 声明 `go 1.20`
- 重点维度：L02 高并发、L06 反射与接口、L07 unsafe/零拷贝；兼顾 L05 分配/GC、L11 编译优化、L12 初始化、L18 写屏障、L19 netpoll/定时器/优雅关闭
- 测试规模：61 个 `*_test.go`，共 26,067 行；精读 28 个文件（完整或核心段），其余仅按函数名/关键字 grep 抽样
- 构建/依赖提示：测试无 GOOS/GOARCH 构建标签，唯一构建约束为 `helpers_fuzz_test.go` 的 `//go:build go1.18`。依赖 fasthttp v1.51.0（`*fasthttp.RequestCtx`、`fasthttputil.NewInmemoryListener`）、bytebufferpool、google/uuid、tinylib/msgp、andybalholm/brotli、tcplisten 等；场景重写须替换为 `net/http`、`net.Pipe`、`sync.Pool` 和纯内存结构。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| app_test.go:1623 | 两个 `t.Parallel` 子测试各自 `New(Config{Immutable:true})` 后并发 `app.Test`，配置与路由只读共享 | L02/L06 | 低 | `net/http/httptest` + 只读路由表；多 goroutine 共享不可变 handler，`sync.WaitGroup` 收尾 |
| app_test.go:1955 | ctx 对象池 Acquire/Release 两条路径（复用/新建 RequestCtx）基准，观察分配与复用 | L05/L18 | 低 | `sync.Pool` 存 ctx 结构体，Reset 后 Put；循环中插 `runtime.GC()`，校验旧值不残留 |
| app_test.go:763 | 内存 listener 上挂 5s 慢 handler，`ShutdownWithTimeout(1s)` 必须返回 `context.DeadlineExceeded` | L19/L02 | 中 | `net.Pipe` + 极简关闭协议；`context.WithTimeout` + `select` 实现超时关闭 |
| app_test.go:1702 | handler 内 `runtime.Goexit()`，`app.Test(req,-1)` 不得永久挂起 | L02/L19 | 中 | goroutine 内 `runtime.Goexit` + done channel + `select/time.After`；确保 defer 清理 |
| ctx_test.go:895 | 10 次连续请求复用 Ctx，`UserContext` 值不得跨请求泄漏 | L18/L06 | 低 | `sync.Pool` Ctx + `context.WithValue`；每轮 GC 后断言 `Value` 为空 |
| ctx_test.go:4854 | 反射把 header 绑定到 struct，含逗号拆分 slice、覆盖已有值、`required` 报错 | L06/L13 | 中 | `reflect` 遍历字段/tag；`strings.Split` 填 slice；缺失 required 返回固定错误 |
| ctx_test.go:4521 | 反射 query 绑定，支持重复 key/逗号拆分、空值、括号不匹配错误 | L06/L13 | 中 | 纯 `net/url.Values` + reflect 绑定；错误路径返回固定字符串 |
| ctx_test.go:5170 | `equalFieldType` 对字段名/标签/类型做反射匹配（大小写不敏感） | L06/L11 | 低 | 缩小版 reflect 元数据匹配；表驱动，GC 后重复调用 |
| ctx_test.go:1418 | 同一 struct 被 Body/Cookie/Query/Params/ReqHeader 五种 parser 填充，JSON/XML/form/multipart 子测试并行 | L06/L02 | 中 | 统一 binder + 多来源 map；并行子测试，固定输入值 |
| ctx_test.go:4190 | `SendStream` 接受 bytes.Reader/bufio.Reader 并写出响应体 | L07/L05 | 低 | `io.Copy` 到 bytes.Buffer；验证不改变源数据，GC 后内容一致 |
| ctx_test.go:5381 | `SetBodyStreamWriter` 分两段写入并 Flush，响应体顺序正确 | L07/L05 | 低 | `bufio.Writer` + 分块 Flush 到 buffer；流打开期间触发 GC |
| router_test.go:306 | 通过 interface 断言确认 `*App`/`*Group` 都实现 `Router` | L06 | 低 | 自定义 `Router` 接口 + 两种实现；type switch/断言，GC 后仍可调用 |
| router_test.go:894 | `RunParallel` 多 goroutine 共享 app handler，反复分发根路由 | L02/L11 | 中 | 不可变路由树 + `sync.WaitGroup`；atomic 计数，GC 插在批次间 |
| router_test.go:651 | 每次迭代 `New()` + 注册路由 + `startupProcess()`，重建路由栈 | L12/L11 | 中 | 包级 `sync.Once` 初始化路由表 + 每轮复制；比较路由数/栈长度 |
| router_test.go:25 | `init()` 读 `testRoutes.json` 并 `json.Unmarshal` 到包级变量，供全文件共享 | L12/L06 | 中 | 内联固定路由 fixture，在 `init()` 构建；断言初始化顺序与只读性，不读文件 |
| path_test.go:138 | 表驱动 `parseRoute/getMatch`，固定 `[maxParams]string` 参数数组，覆盖通配/可选/转义 | L11/L13 | 中 | 简化前缀树 + 定长参数数组；无分配匹配；每 N 轮 GC |
| utils/convert_test.go:19 | `UnsafeString([]byte)` 与 `string()` 的基准对比，零拷贝别名 | L07/L11 | 低 | `unsafe.String`/`unsafe.Slice`；修改源切片验证别名可见；GC 后仍有效 |
| utils/convert_test.go:44 | `UnsafeBytes(string)` 与 `[]byte(string)` 基准对比，零拷贝只读视图 | L07/L11 | 低 | `unsafe.Slice(unsafe.StringData(s), len(s))`；只读校验；与拷贝版对比分配 |
| middleware/adaptor/adaptor_test.go:466 | 50MB 请求体经 net/http→fiber handler 转换，基准反复执行 | L05/L07 | 高 | 单 main 生成 8/16MB payload，`io.ReadAll` + JSON/长度校验；大对象分配后 GC |
| internal/memory/memory_test.go:56 | 1000 个 UUID key 的 Set/Get/Delete 基准，带 TTL | L05/L18/L19 | 中 | `map[string]entry` + 过期时间；批量插入/读取/删除；GC 检查 map 缩小 |
| internal/storage/memory/memory_test.go:52 | 带 TTL 写入后 sleep 1.1s，`Get` 必须过期返回空 | L19/L18 | 低 | 注入 fake clock 的 TTL map；无 sleep，直接推进虚拟时间；删除后 GC |
| middleware/limiter/limiter_test.go:65 | 50 goroutine 并发请求，达到 Max 后 429，2s 过期后恢复 | L02/L19 | 中 | mutex/atomic 固定窗口计数 + 虚拟时钟；并发 50 次，断言 429/200 |
| middleware/session/session_test.go:802 | 10 goroutine 各自 Acquire Ctx，Set/Save/Get/Delete/Destroy session | L02/L18 | 高 | 内存 session store + 每 session 锁；并发 CRUD，校验 ID 唯一且无残留 |
| middleware/logger/logger_test.go:400 | 两个 logger 中间件共享 bytebufferpool，同时 app.Test 请求，检测数据竞争 | L02/L18 | 中 | `sync.Pool` 缓冲 + `sync.Mutex` 日志写出；并发 2 请求，GC 后缓冲可复用 |
| middleware/cache/cache_test.go:712 | MaxBytes=2 时按插入/访问顺序 LRU 淘汰，X-Cache 命中/未命中序列固定 | L13/L05/L18 | 中 | `map+list` LRU，字节配额；断言命中序列；淘汰后 GC 检查旧值不可达 |
| middleware/timeout/timeout_test.go:16 | 中间件 100ms context 超时；300/500ms 请求返回 408，50/30ms 成功 | L19/L02 | 中 | `context.WithTimeout` + handler 用 `select` 等待；用 channel 触发替代真实 sleep |
| utils/time_test.go:17 | 后台 goroutine 每秒刷新 `atomic.Uint32` 时间戳，读取端无锁 | L19/L02 | 低 | `time.Ticker` + atomic；fake 时钟推进；停止 ticker 后 GC |
| client_test.go:961 | 同一 Agent 两次请求复用连接，经内存 listener 返回相同 body | L02/L19 | 中 | `net.Pipe` + `http.Server` + `http.Client` keep-alive；两次请求计数连接 |
| client_test.go:616 | `RetryIf` 在 3 次 dial 失败后第 4 次成功 | L02/L19 | 中 | 自定义 `RoundTripper` 前 3 次返回错误、第 4 次成功；断言调用次数 |
| hooks_test.go:184 | `OnShutdown` 注册的回调在 `Shutdown()` 时执行一次 | L12/L19 | 低 | 包级 hook 切片 + `sync.Once`；调用顺序/次数断言，GC 后 hook 可释放 |
| mount_test.go:56 | 三层子 App 依次 Mount，路由 `/one/two/three/test` 可达，handlers/routes 计数固定 | L12/L06 | 中 | 嵌套路由表合并 + 前缀剥离；断言路径/计数；GC 后子表不丢 |

## 推荐收录用例

以下 15 条均按“单包 `main`、仅标准库、可独立编译、输出确定（末行 `ALL PASS <用例名>`）”设计；GC 明确落在池化/长生命周期/大对象位置。

1. `l02_parallel_app_test_immutable` — 来源 `app_test.go:1623`。场景与做法：两个并行子测试各自构造不可变配置 app，再并发发请求，验证路由/配置只读共享；用 64 goroutine 各 200 次请求、atomic 汇总。维度 L02/L06；复杂度 低；优先级 P1。stdlib 重写：`net/http/httptest` + 只读路由表，`sync.WaitGroup` 收尾；每批后 `runtime.GC()`；只输出固定计数。
2. `l02_router_dispatch_parallel` — 来源 `router_test.go:894`。场景与做法：`RunParallel` 共享同一 handler 反复分发根路由；重写为 64 goroutine × 1000 次固定路径匹配，atomic 计数并核对参数。维度 L02/L11；复杂度 中；优先级 P1。stdlib 重写：不可变前缀树 + 定长参数数组；每 10000 次 GC；输出固定命中数。
3. `l02_limiter_concurrent_quota` — 来源 `middleware/limiter/limiter_test.go:65`。场景与做法：50 goroutine 并发打满配额后第 51 个请求 429，窗口过期后恢复 200。维度 L02/L19；复杂度 中；优先级 P1。stdlib 重写：mutex 计数 + 手动 `now` 虚拟时钟，不 sleep；并发 50 次，断言 429/200 序列。
4. `l02_session_concurrent_lifecycle` — 来源 `middleware/session/session_test.go:802`。场景与做法：10 goroutine 各自创建 session，完成 Set/Save/Get/Delete/Destroy 全套生命周期并校验 ID 唯一、互不串扰。维度 L02/L18；复杂度 高；优先级 P2。stdlib 重写：`map` + 每 session `sync.Mutex`；GC 后断言 map 长度归零，输出固定 ID 计数。
5. `l05_ctx_pool_acquire_release` — 来源 `app_test.go:1955`。场景与做法：ctx 池 Acquire/Release 两条路径交替，验证复用对象字段清零。维度 L05/L18；复杂度 低；优先级 P1。stdlib 重写：`sync.Pool` + `Reset`，复用/新建两种来源；每 1000 次 GC；校验无旧值泄漏，输出固定次数。
6. `l05_adaptor_large_body` — 来源 `middleware/adaptor/adaptor_test.go:466`。场景与做法：50MB 请求体经过 net/http→框架转换，反复执行。维度 L05/L07；复杂度 高；优先级 P2。stdlib 重写：单 main 生成 8/16MB payload，`io.ReadAll` + JSON 解码 + 长度/校验和；分配后 `runtime.GC()`；固定 payload 保证输出确定。
7. `l06_struct_parser_reflect` — 来源 `ctx_test.go:4854`（同类可看 `ctx_test.go:4521`、`ctx_test.go:5749`）。场景与做法：反射把 header/query/body 绑定进带 tag 的 struct，覆盖 slice 拆分、required 错误和覆盖写入。维度 L06/L05；复杂度 中；优先级 P1。stdlib 重写：自写 tag 解析 + reflect 字段遍历；循环 200 次并 GC；固定输入和错误串。
8. `l06_router_interface_assert` — 来源 `router_test.go:306`。场景与做法：确认 `*App` 与 `*Group` 都实现 `Router` 接口。维度 L06；复杂度 低；优先级 P0。stdlib 重写：定义 `Router` 接口与两种实现，`var _ Router`、type switch、接口方法调用；GC 后重复断言，输出固定 true。
9. `l07_unsafe_string_bytes` — 来源 `utils/convert_test.go:19`（另见 `utils/convert_test.go:44`）。场景与做法：`UnsafeString`/`UnsafeBytes` 与拷贝版对比，验证零拷贝别名与分配差异。维度 L07/L11；复杂度 低；优先级 P1。stdlib 重写：`unsafe.String`/`unsafe.Slice`；修改源切片观察别名；各 100k 次并插 GC；输出长度与内容。
10. `l07_stream_writer_flush` — 来源 `ctx_test.go:5381`。场景与做法：分块写入并 Flush，校验输出顺序与总长度。维度 L07/L05；复杂度 低；优先级 P1。stdlib 重写：`bufio.Writer` 分块 Flush 到 buffer；流未关闭时 GC；逐块比对固定字符串。
11. `l11_route_match_hotpath` — 来源 `path_test.go:138`（基准见 `path_test.go:210`）。场景与做法：表驱动路径匹配，固定 `[maxParams]string` 参数槽，覆盖通配/可选/转义。维度 L11/L13；复杂度 中；优先级 P1。stdlib 重写：简化前缀树 + 定长参数数组；100 条路由表驱动，每轮 GC；与 golden 比对 match/params。
12. `l12_init_route_fixture` — 来源 `router_test.go:25`。场景与做法：`init()` 解析 JSON fixture 填充包级路由表，供全文件共享。维度 L12/L06；复杂度 中；优先级 P1。stdlib 重写：包级 `var` 字面量 + `init()` 构建索引，`sync.Once` 复制；断言初始化顺序、只读共享与深拷贝；无文件 IO。
13. `l18_usercontext_pooled_reuse` — 来源 `ctx_test.go:895`。场景与做法：池化 Ctx 连续处理 10 个请求，`UserContext` 值不得跨请求残留。维度 L18/L06；复杂度 低；优先级 P1。stdlib 重写：`sync.Pool` + `context.WithValue`；每轮 GC；断言首轮无值、每轮值独立，输出固定值。
14. `l19_shutdown_deadline` — 来源 `app_test.go:763`。场景与做法：慢 handler 占住连接时 `ShutdownWithTimeout(1s)` 必须返回 `context.DeadlineExceeded`。维度 L19/L02；复杂度 中；优先级 P1。stdlib 重写：`net.Pipe` 上的极简 HTTP 服务；handler 阻塞在 channel；`ShutdownWithContext(100ms)` 断言超时；释放 channel；全程 channel 同步，不用 sleep。
15. `l19_client_reuse_timeout_retry` — 来源 `client_test.go:961`（重试见 `client_test.go:616`、超时见 `client_test.go:935`）。场景与做法：同一 Agent 复用长连接完成两次请求；另两路分别验证 50ms 超时打断慢服务、前 3 次连接失败后第 4 次成功。维度 L19/L02；复杂度 中；优先级 P1。stdlib 重写：`net.Pipe` + `http.Server` + `http.Client` keep-alive，统计连接建立次数；自定义 `RoundTripper` 前 3 次返回错误、第 4 次成功；`http.Client{Timeout:50ms}` 打阻塞 server；断言调用次数与超时错误，全部 channel 驱动。

## 不可移植清单

- **真实 TLS/mTLS**：`listen_test.go:50`、`listen_test.go:80`、`client_test.go:992`、`client_test.go:1026`、`middleware/proxy/proxy_test.go:106`。依赖 `.github/testdata` 证书、`crypto/tls` 握手与握手时序；可用 stdlib 内嵌证书近似，但随机数与超时不稳定，不建议作为运行级用例。
- **prefork/多进程**：`prefork_test.go:17`、`prefork_test.go:52`、`listen_test.go:41`、`listen_test.go:68`、`listen_test.go:98`、`hooks_test.go:227`。依赖环境变量、子进程 exec、`tcplisten`/SO_REUSEPORT 与信号；单包 main 无法复刻，不可移植。
- **固定端口/真实回环**：`listen_test.go:26`（:4003）、`hooks_test.go:202`（:9000）、`app_test.go:1502`（:4004）。可改为 :0 或 `net.Pipe`/回环后重写；原样存在端口冲突和 sleep 时序，属“可改写但不应照搬”。
- **fasthttputil 内存 listener**：`listen_test.go:110`、`client_test.go:961`、`app_test.go:763`、`app_test.go:803`。第三方 listener，但语义等价于 `net.Pipe`；推荐改写为 `net.Pipe` + `net/http`。
- **真实上游代理与时序**：`middleware/proxy/proxy_test.go:20`、`middleware/proxy/proxy_test.go:83`、`middleware/proxy/proxy_test.go:284`。`net.Listen` + 2 秒 sleep + 真实转发；可换成 `httptest`/`net.Pipe` 近似，但代理语义完整复刻成本高，建议只借鉴超时与转发场景。
- **Brotli**：`middleware/compress/compress_test.go:108`。依赖第三方 `andybalholm/brotli`，stdlib 无对应实现；场景可改用 gzip 替代，但原算法不可 stdlib 重写。
- **测试资产文件**：`router_test.go:25`（`testRoutes.json`）、`middleware/compress/compress_test.go:17`（`../../.github/README.md`）、`path_testcases_test.go:28`（大表 init）。数据可内联，属“可改写”；原样依赖工作目录和相对路径。
- **代码生成/第三方序列化**：`middleware/idempotency/response_msgp_test.go:3`。`tinylib/msgp` 生成代码，不属于本库重点维度，跳过。
- **随机数/真实时钟**：`utils/common_test.go:23`（UUID）、`middleware/csrf/csrf_test.go:161`（1.25s 过期 token）、`middleware/encryptcookie/encryptcookie_test.go:14`（`GenerateKey()`）。输出不确定，须固定随机源/注入 fake clock 后才能改为确定性用例；原样不可移植。
- **Fuzz/构建约束**：`helpers_fuzz_test.go:1`、`helpers_fuzz_test.go:10`。`//go:build go1.18` 加 fuzz 引擎，不是运行级稳定性用例。
- **服务器生命周期错误文本**：`app_test.go:744`。`Shutdown` 的 `"shutdown: server is not running"` 依赖 fasthttp 内部状态；可重写关闭语义，但不应断言该字符串。
- **Ctx 与 TLS 类型耦合**：`ctx_test.go:2097`。仅构造 `tls.ClientHelloInfo`，场景可移植，但入口依赖 fasthttp TLS handler 字段；可改为直接构造 stdlib 类型，原样不宜搬运。

## 核验记录

- 实际 grep 关键词：`concurrent|goroutine|pool|alloc|unsafe|noescape|string|bytes|reflect|interface|bind|reuse|close|timeout|cancel|race|append|map|slice|chan|select|shutdown|graceful|prefork`；补充检索 `t.Parallel|sync.|atomic.|runtime.GC|func init|go:build|func Test|func Benchmark`。
- 版本复核：`git describe --tags --exact-match` → `v2.52.6`；`git rev-parse HEAD` → `e04f815c43cb8a84f4de90e1d1bd9507bc33fa8c`；`go.mod` 第 3 行为 `go 1.20`。
- 精读 28 个测试文件（完整或核心段）：`app_test.go`、`ctx_test.go`、`router_test.go`、`path_test.go`、`path_testcases_test.go`、`listen_test.go`、`prefork_test.go`、`hooks_test.go`、`mount_test.go`、`client_test.go`、`helpers_test.go`、`internal/memory/memory_test.go`、`internal/storage/memory/memory_test.go`、`utils/convert_test.go`、`utils/bytes_test.go`、`utils/common_test.go`、`utils/time_test.go`、`middleware/limiter/limiter_test.go`、`middleware/session/session_test.go`、`middleware/logger/logger_test.go`、`middleware/cache/cache_test.go`、`middleware/timeout/timeout_test.go`、`middleware/compress/compress_test.go`、`middleware/adaptor/adaptor_test.go`、`middleware/proxy/proxy_test.go`、`middleware/etag/etag_test.go`、`middleware/recover/recover_test.go`、`middleware/requestid/requestid_test.go`。其余 33 个测试文件只做函数名/关键字 grep。
- 行号复核：本文所有 `file:line` 均以 `sed -n '<line>p'` 或 `grep -n` 回查；文件存在、行号不超过 `wc -l`，且该行落在对应 `Test/Benchmark/init` 函数或关键语句上。
