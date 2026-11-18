# echo v4.13.4

- 来源：`research/repos/echo`（partial clone，仅 `*_test.go` / `_fixture` / `go.mod` / `README.md`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/echo`
- 模块/版本核验：`github.com/labstack/echo/v4`；`git tag` = `v4.13.4`，HEAD `98ca08e7dd64`（短哈希 `98ca08e7`，2025-05-22，`Improve changelog for 4.13.4 (#2783)`）；`go.mod` 声明 `go 1.23.0`
- 重点维度：L02 高并发（context 池、超时/限流中间件、并发请求）、L06 反射与接口（Binder/Unmarshaler/HTTPError/路由注册）、L05/L18 分配与池化（context pool、sync.Pool）、L11 编译优化（表驱动、反射绑定开销）、L19 netpoll/定时器/优雅关闭；L07 unsafe 仅 `middleware/logger_test.go:175` 一处 `*json.RawMessage` → `*string` 重解释，其余为零拷贝邻近场景（stream/io.Copy/gzip 直写）
- 测试规模：39 个 `*_test.go`，约 20,566 行；408 个 `Test*` + 35 个 `Benchmark*`；本次精读 25 个测试文件（见核验记录）
- 构建标签/依赖提示：测试无 `//go:build` 标签；除 `binder_external_test.go` 为 `package echo_test` 外，其余均为 `package echo`，直接使用 `e.pool`、`e.maxParam`、`router.Find` 等未导出成员，不能原样搬入独立 `main`。测试侧三方依赖：`stretchr/testify`（断言）、`labstack/gommon/log`（Logger/Recover 断言）、`golang.org/x/net/http2`（h2c）、`golang.org/x/net/websocket`（代理 WebSocket）、`golang.org/x/time/rate`（限流）。partial clone 的 `_fixture` 只剩两个 README，PNG/证书/脚本全部缺失，静态文件类用例不能直接运行；用例一律按 stdlib 重写

## 场景表

| 测试文件:行 | 场景摘要 | 对应维度 L01–L19 | 复杂度 低/中/高 | stdlib 重写思路 |
|---|---|---|---|---|
| `echo_test.go:313` | `Pre` + 3 层 `Use` 的闭包执行顺序（输出 `-1123`），Pre 阶段 `c.Path()` 为空 | L02/L06/L18 | 中 | 用 `func(http.Handler) http.Handler` 切片手写组合链；GC 插在请求循环中，校验顺序符 |
| `echo_test.go:602` | `%2F` 在路由阶段不解码，`:id` 参数保留原始转义串 | L07/L13 | 中 | 直接切 `Request.URL.EscapedPath()` 子串，比较参数与原始串是否别名 |
| `echo_test.go:889` | `AcquireContext` / `ReleaseContext` 从池取还 `*context` | L05/L18 | 低 | 自建 `sync.Pool` 包装请求上下文，取还之间穿插 `runtime.GC()` |
| `echo_test.go:926` | `Start(":0")` 在 goroutine 监听，主 goroutine 用 ticker+select 轮询 `ListenerAddr` 后 `Close` | L19/L02 | 中 | `net.Listen("tcp","127.0.0.1:0")` + `http.Server.Serve`，channel 通知就绪、select 等待 |
| `echo_test.go:1220` | 测试助手用 `reflect.ValueOf(e).MethodByName("GET").Call(...)` 批量注册 HTTP 方法 | L06/L11 | 中 | `reflect.MethodByName` 调本地结构体方法，与直接调用对照结果/分配 |
| `echo_test.go:1238` | `HTTPError` 文本格式 `code=..., message=map[...]`；`SetInternal`/`WithInternal` 追加 internal | L06/L05 | 低 | 自写 `HTTPError` + `Error()`，map payload 排序后格式化 |
| `echo_test.go:1302` | 10 组错误形态（普通 error、HTTPError、自定义 `MarshalJSON`、已提交响应、internal）在 Debug on/off 下的 JSON 响应 | L06/L05/L11 | 中 | 表驱动 + `encoding/json` 编码 `map[string]any`，分别验证 pretty/紧凑输出 |
| `echo_test.go:1427` | `Close()` 重复调用幂等；`Start` 最终返回 `http: Server closed` | L19 | 低 | `http.Server.Shutdown` 二次调用，channel 收尾错误 |
| `echo_test.go:1448` | `Shutdown(ctx)` 带 10s 超时关闭已启动的 server | L19/L02 | 中 | `context.WithTimeout` + `Server.Shutdown`，统计 in-flight handler 完成数 |
| `echo_test.go:1556` | `OnAddRouteHandler` 回调捕获注册信息；handler 名取运行时调用栈（含完整包路径后缀） | L06/L11 | 低 | `runtime.Caller` / `reflect.ValueOf(fn).Pointer()` 取函数名，只比较后缀保证确定 |
| `context_test.go:34` | `BenchmarkAllocJSONP`：同一 context 反复 JSONP 编码并统计 allocs | L05/L11 | 低 | 循环 `json.Marshal` + `bytes.Buffer`，用 `testing.AllocsPerRun` 比较复用/不复用缓冲 |
| `context_test.go:402` | `c.Stream` 把 `strings.Reader` 内容经 `io.Copy` 写入响应并设 Content-Type | L07/L19 | 低 | `io.Copy(httptest.NewRecorder(), strings.NewReader(...))`，校验字节与 header |
| `context_test.go:508` | `Reset` 清空 param names/values/path/query/store | L18/L06 | 低 | 池化上下文 `Reset(req, rec)`，置空切片并复用 map 容量 |
| `context_test.go:656` | `AcquireContext` 复用 + `SetParamNames/Values` 长度不匹配时按 maxParam 扩容/截断 | L18/L06/L13 | 中 | 手写 paramNames/paramValues 双切片 + maxParam 上限，池取还 + GC |
| `router_test.go:951` | 6 条 static/param/any 混合路由的多级回溯，`/a/x/f` 命中 `/a/*/f` | L11/L13 | 中 | 手写压缩前缀 trie（static/param/any 三类子节点），固定路由表与期望表 |
| `router_test.go:1368` | 插入 `/te/st*` 触发节点拆分，`RouteNotFound` handler 必须随节点迁移保留 | L11/L13 | 高 | 手写 trie 节点分裂，校验 NotFound handler 与 ID 不丢失 |
| `router_test.go:2529` | handler 内少赋值 param 后继续 `Find`，不得 panic/死循环 | L18/L13 | 中 | 固定长度参数槽位 + 计数，读取前判界；recover 捕获 panic 转 FAIL |
| `router_test.go:2832` | 路由匹配基准：每轮 `e.pool.Get()` 取 context、`Find` 后 `Put` | L05/L18 | 低 | `sync.Pool` + 手写匹配循环，`AllocsPerRun` 比较池化前后 |
| `bind_test.go:177` | 嵌套结构体 + tag：`/?id=1&ID=2`，带 tag 的嵌套字段被填，无 tag 字段不填 | L06/L11 | 低 | `reflect` 递归遍历字段、读 `query` tag、`strings.EqualFold` 匹配 |
| `bind_test.go:336` | `encoding.TextUnmarshaler`（`time.Time` / `*time.Time`）从 query 绑定 | L06 | 低 | `reflect.Type.Implements(TextUnmarshaler)` + `UnmarshalText` |
| `bind_test.go:442` | `bindData` 到 `map[string]string` / `map[string][]string` / `map[string]interface{}`，nil map 需初始化；`map[string]int` 跳过 | L06/L13 | 中 | `reflect.MakeMap` 分支；nil map 与接口值各测一轮 |
| `bind_test.go:663` | `DefaultBinder.bindData` 对 20+ tag 字段绑定的基准，纯反射分配 | L05/L11 | 低 | `reflect` 字段遍历 + `strconv` 转换，GC 插在每轮前后 |
| `bind_test.go:755` | 混合来源优先级：path/query/body 按 method 分阶段覆盖 | L06/L11 | 中 | 固定顺序的 source 列表逐源反射写字段，14 组优先级期望 |
| `binder_test.go:2102` | `BindUnmarshaler`（自定义 `UnmarshalParam`）绑定 + FailFast + Must 语义 | L06/L05 | 中 | 自写 QueryBinder 方法链，接口断言 + 固定错误表 |
| `binder_test.go:2193` | `json.Unmarshaler`（`*big.Int`）从 query 绑定，错误信息含 `math/big` 反序列化失败 | L06/L05 | 低 | `big.Int.SetString` + 自定义 `UnmarshalJSON`；错误文本由用例定死 |
| `binder_test.go:2968` | 10 字段 struct 用反射 Binder 每轮绑定，统计 allocs | L05/L11 | 低 | `reflect` 逐字段 `strconv`，`AllocsPerRun` |
| `group_test.go:47` | 同一 Group 上 3 个全局中间件 + 每路由中间件，验证 middleware 切片不被复用/串扰 | L02/L18 | 低 | 每条路由复制一份 `[]Middleware` 再组合，比较长度与执行序列 |
| `response_test.go:77` | 响应包装器不支持 `http.Flusher` 时 `Flush` panic 固定错误 | L06/L19 | 低 | 自定义 `ResponseWriter`（不实现 Flusher），类型断言 + recover 校验 |
| `json_test.go:53` | `json.SyntaxError` / `UnmarshalTypeError` 映射为 400 HTTPError，错误串含 offset/field | L06 | 低 | `json.Decoder` 解码，`errors.As` 提取错误位置 |
| `middleware/logger_test.go:175` | `*(*string)(unsafe.Pointer(rawMsg))` 把 `*json.RawMessage` 零拷贝重解释为 string | L07/L06/L18 | 低 | 同样 `unsafe.Pointer` 转换 vs `string(*raw)` 拷贝对照，`runtime.KeepAlive` |
| `middleware/timeout_test.go:204` | 1ms timer 与 ~1ms handler 竞争，响应 200 或 503，内容必须自洽 | L02/L19/L18 | 中 | 改写为 channel 同步：handler 阻塞到 timer 触发，断言恰好一种结果 |
| `middleware/timeout_test.go:332` | 完整 server + Logger + Recover + Timeout，handler 永久阻塞，检查 503 且不出现 `superfluous WriteHeader` | L19/L02 | 高 | `httptest.NewServer` + goroutine 阻塞；用 `server.Config.ErrorLog` 捕获日志 |
| `middleware/decompress_test.go:154` | 自定义 `sync.Pool` 产出 error，Decompress 返回 500 且请求体原样保留 | L18/L05 | 中 | `sync.Pool` + `gzip.NewReader`，池中放错误值，类型断言失败路径不得 panic |
| `middleware/decompress_test.go:173` | 每请求从池取 gzip reader、解压、Reset 后归还的基准 | L05/L18 | 低 | `sync.Pool` 包 `*gzip.Reader`，循环 Get/Reset/Read/Put，周期 GC |
| `middleware/rate_limiter_test.go:352` | `map[string]*Visitor` 过期清理：保留最后访问 ≈0/1min，删除 ≥5min | L18/L05 | 中 | map + 注入假时钟，遍历删除，固定期望键集合 |
| `middleware/rate_limiter_test.go:456` | 100 goroutine × 10000 地址并发 `Allow`，压 token bucket 存储 | L02/L05 | 中 | `sync.Mutex` + 自实现 token bucket，WaitGroup + 计数守恒校验 |
| `middleware/util_test.go:125` | 10 万次随机字符串，统计固定字符集（`randomStringCharsetLen`）出现频率偏差 ±5% | L05/L13 | 中 | `crypto/rand` 取样 + `map[byte]int`；固定样本量做统计检验 |
| `middleware/body_dump_test.go:18` | BodyDump 包装请求/响应 body，回调拿到完整 `[]byte`（TeeReader 风格） | L07/L05 | 中 | `io.TeeReader` + 自定义 ResponseWriter 缓冲，比较两份 body |
| `middleware/proxy_test.go:717` | 客户端取消 context 后代理返回 499，upstream 阻塞直至取消 | L02/L19 | 高 | 直接调用 handler + `context.WithCancel`，channel 控制 upstream 放行 |
| `middleware/compress_test.go:19` | gzip 中间件分块写 + `Flush`，对端用 `gzip.Reader` 逐 5 字节读取 | L07/L19 | 中 | `gzip.Writer` + `httptest.ResponseRecorder.Flush`，分块校验 |
| `ip_test.go:22` | `TrustOption` 组合（loopback/link-local/private/CIDR）对 IPv4/IPv6 的信任判定 | L06/L11 | 低 | `net.ParseCIDR` + `net.IPNet.Contains`，表驱动 |
| `context_fs_test.go:15` | `File` 从默认 FS 或 `fs.FS` 读 PNG，只校验前 3–4 字节魔数；不存在返回 404 | L05/L07 | 低 | `fstest.MapFS` + `io.ReadAll`，按魔数校验，不依赖磁盘 fixture |

## 推荐收录用例（按优先级排序）

以下用例均按“单包 `main` + 仅标准库”设计；GC 位置显式写出，输出只含计数/固定字符串，最后一行 `ALL PASS <用例目录名>`。

### 1. `l18_context_pool_reuse_gc`（源 `context_test.go:656`、`echo_test.go:889`、`router_test.go:2850`）
- 场景：`sync.Pool` 中放上下文结构体（`paramNames/paramValues []string`、`store map[string]any`、`query url.Values`）；8 worker × 2000 轮 Acquire → 设 3 个参数 → 存 2 个键 → 读取校验 → Reset → Release。
- GC 位置：每轮前后 `runtime.GC()`；每 128 轮主 goroutine 再 GC；参数槽位故意跨 2→4→8 容量边界触发扩容/复用。
- 期望：Reset 后 names/values 长度 0、store 空，无上一轮残留；参数值恒等；无 panic/race。
- 维度：L18/L05/L02；复杂度：中；优先级：P0（默认规模 <2s，`TORTURE_*` 放大到 64×20000 作 P2）。
- stdlib 重写：仅 `sync.Pool` + `context` + `runtime`；参数名/值用固定上限 8 的数组式切片复用；map 用 `clear` 保留容量。

### 2. `l02_router_find_concurrent`（源 `router_test.go:951`、`router_test.go:2832`）
- 场景：构建 6 条 static/param/any 混合路由（`/a/:b/c`、`/a/c/d`、`/a/c/df`、`/a/*/f`、`/:e/c/f`、`/*`），N 个 goroutine 并发 `Find` 固定 URL 集合，校验命中路由与参数；路由表构建后只读。
- GC 位置：每 worker 每 100 次 GC；另起 goroutine 持续分配 1MB 垃圾制造并发标记；参数切片复用。
- 期望：每条 URL 命中固定路由与固定参数（与源表一致）；`Find` 不写共享节点。
- 维度：L02/L11/L13；复杂度：中；优先级：P1（`TORTURE_WORKERS` 放大 → P2）。
- stdlib 重写：手写压缩前缀 trie（static/param/any 三类边）；上下文用局部 slice/map；只输出命中计数。

### 3. `l06_reflect_bind_tags`（源 `bind_test.go:177`、`bind_test.go:755`）
- 场景：反射 Binder，支持 `query`/`form`/`header`/`param` tag、嵌套 struct、大小写不敏感匹配；按 path→query→body 分阶段写字段（模拟 Echo 覆盖顺序，header 单独绑定）。
- GC 位置：每轮绑定后 GC；目标 struct 与新分配的 tag 缓存 map 交替存活/丢弃。
- 期望：固定输入得到固定字段值；未带 tag 的子字段保持零值；未知 tag 忽略；path/query/body 覆盖顺序与源表一致。
- 维度：L06/L11/L05；复杂度：中；优先级：P0。
- stdlib 重写：`reflect.TypeOf/ValueOf` 遍历（含匿名字段指针），`strings.EqualFold` 匹配，`strconv` 解析基础类型；不用 validator。

### 4. `l06_bind_unmarshaler_dispatch`（源 `binder_test.go:2102`、`binder_test.go:2284`、`binder_test.go:2193`）
- 场景：query 值按接口分派：`BindUnmarshaler`（自定义 `UnmarshalParam`）→ `encoding.TextUnmarshaler` → `json.Unmarshaler`，覆盖值/指针接收者；Must/FailFast 错误累积。
- GC 位置：每次绑定成功/失败后 GC；保留接口值（含 `big.Int` 指针）跨 GC 再断言。
- 期望：时间串 → `time.Time`/`Timestamp`；`big.Int` 得 999；错误字段名/文本固定；fail-fast 后不覆盖旧值。
- 维度：L06/L05；复杂度：中；优先级：P1。
- stdlib 重写：`reflect` 检测接口实现；`time.Parse`、`big.Int.SetString`、`json.Unmarshal`；错误链用 `errors.As`。

### 5. `l06_http_error_chain`（源 `echo_test.go:1302`、`echo_test.go:1264`）
- 场景：构造 `HTTPError{Code, Message, Internal}`，覆盖 `SetInternal`/`WithInternal`/`Unwrap`；错误处理器在 Debug on/off 下输出 JSON；自定义 `MarshalJSON` 错误透传。
- GC 位置：每个子用例前 GC；把 `*HTTPError` 放进切片跨 GC 后 `errors.Unwrap/Is`。
- 期望：状态码与 body 与源表一致（含 `{"message":"Internal Server Error"}`）；已提交响应不再追加。
- 维度：L06/L05；复杂度：中；优先级：P1。
- stdlib 重写：`encoding/json` 编码 `map[string]any`，`errors.Unwrap` 保链；直接调用处理函数，不启服务。

### 6. `l02_middleware_chain_slice`（源 `echo_test.go:313`、`group_test.go:47`）
- 场景：3 个全局中间件 + `Pre` + 每路由中间件组合，验证执行顺序与切片隔离；多个 Group 共享中间件时不串写。
- GC 位置：每次请求构造新 Context 后 GC；中间件闭包持有的 `*bytes.Buffer` 跨 GC。
- 期望：顺序符 `-1123`；路由级中间件只影响自身；并发 100 请求各自顺序一致。
- 维度：L02/L06/L18；复杂度：低；优先级：P0。
- stdlib 重写：`type MW func(http.Handler) http.Handler` 手写 Compose；每条路由 `append([]MW(nil), groupMW...)` 复制；`httptest.NewRecorder` 校验。

### 7. `l19_graceful_shutdown`（源 `echo_test.go:1448`、`echo_test.go:926`、`echo_test.go:1427`）
- 场景：`net.Listen("tcp","127.0.0.1:0")` 启 `http.Server`，handler 阻塞在 channel；主 goroutine 发请求后 `Shutdown(ctx)`，再放行 handler；验证重复 `Shutdown/Close` 幂等。
- GC 位置：handler 内分配大对象并 GC；Shutdown 前后各 GC 一次。
- 期望：Shutdown 返回 nil；在途请求收到 200；Serve 返回 `http.ErrServerClosed`；第二次 Close 不 panic。
- 维度：L19/L02；复杂度：中；优先级：P1。
- stdlib 重写：`net/http` + `context.WithTimeout` + `sync.WaitGroup`；端口用 `:0`；只输出状态计数。

### 8. `l19_timeout_handler_deterministic`（源 `middleware/timeout_test.go:204`、`middleware/timeout_test.go:332`）
- 场景：复刻 Timeout 中间件：handler 在 goroutine 执行，timer 或完成事件先到者写响应；覆盖“永久阻塞超时”和“按时完成”两分支。
- GC 位置：timeout 分支触发后 GC，检查被放弃 handler 仍持有的大对象；context 取消后 GC。
- 期望：超时分支 503 + 固定 HTML；完成分支 200 + 固定 body；响应只写一次（自定义 Writer 统计 WriteHeader 次数）。
- 维度：L19/L02/L18；复杂度：高；优先级：P1（源测试靠 1ms 竞速；重写改为 channel 同步，确定性）。
- stdlib 重写：`select { case <-done: ... case <-time.After(d): ... }` + 自定义 `ResponseWriter`；先 `cancel()` 再断言。

### 9. `l07_streaming_response_flush`（源 `context_test.go:402`、`middleware/compress_test.go:19`）
- 场景：handler 通过 `io.Copy` 写 3 段数据，每段后 `Flush`，对端逐块读取；再叠加 gzip writer 验证 chunked 流。
- GC 位置：每段后 GC；复用 reader/writer 缓冲。
- 期望：3 段内容/顺序一致；Flush 后数据立即可读；gzip 解压内容一致。
- 维度：L07/L19；复杂度：中；优先级：P1。
- stdlib 重写：`io.Pipe` 或 `net.Pipe` + `bufio.Reader`，`httptest.ResponseRecorder.Flush`，标准库 `compress/gzip`。

### 10. `l07_rawmessage_unsafe_string`（源 `middleware/logger_test.go:175`）
- 场景：`json.Unmarshal` 到 `map[string]*json.RawMessage`，用 `*(*string)(unsafe.Pointer(raw))` 把 `*RawMessage` 直接当 `*string` 读；与 `string(*raw)` 拷贝版对照。
- GC 位置：转换后多次 GC 再读，配合 `runtime.KeepAlive(raw)`；拷贝版对照组。
- 期望：零拷贝串与拷贝串相等且等于预期字符串；GC 后内容不变；拷贝版分配更多。
- 维度：L07/L06/L18；复杂度：低；优先级：P0。
- stdlib 重写：纯 stdlib；`json.RawMessage` 底层是 `[]byte`，该转换读取 slice header 前两字（data,len），场景即验证该布局；输出长度与哈希，不打印地址。

### 11. `l18_decompress_pool_error`（源 `middleware/decompress_test.go:154`、`middleware/decompress_test.go:173`）
- 场景：`sync.Pool` 提供 `*gzip.Reader`；一路正常解压并归还，另一路池返回 error，要求 500 且请求体原样透传。
- GC 位置：每次 Get/Put 间隙 GC；池被清空后再 Get 走 New。
- 期望：解压结果固定；错误池路径不 panic；`rc.Reset` 复用两次输出稳定。
- 维度：L18/L05/L07；复杂度：中；优先级：P1。
- stdlib 重写：`compress/gzip` + `sync.Pool`；池的 New 按开关返回 `*gzip.Reader` 或包着 error 的结构体。

### 12. `l02_request_clone_isolation`（源 `middleware/context_timeout_test.go:103`、`middleware/timeout_test.go:147`）
- 场景：把 `*http.Request` 克隆给子 goroutine（含 body、cookie、form、query），父子分别读写；验证克隆后 body 独立、cookie/query 可读。
- GC 位置：克隆后 GC；子 goroutine 持有请求跨 GC；父请求提前置 nil。
- 期望：子侧读到 cookie=value、form=value、query=value；父侧修改不影响子侧字段。
- 维度：L02/L18/L19；复杂度：中；优先级：P1。
- stdlib 重写：`http.Request.Clone` + `GetBody`/`io.NopCloser`；`sync.WaitGroup` 收尾；不启真实网络。

### 13. `l18_param_slice_bounds`（源 `router_test.go:2529`）
- 场景：router 预分配 maxParam 槽位；handler 内只 `SetParamNames/Values` 1 个值，之后继续匹配含 `:id`/`*` 的路由，验证不读越界、不 loop。
- GC 位置：每次 Find 后 GC；参数切片跨轮复用。
- 期望：`/v1/admin` 得 version=v1；`/v1/view/same-data` 得 `*`=same-data；`/v1/images/view` 得 id=view；无 panic。
- 维度：L18/L13/L06；复杂度：中；优先级：P1。
- stdlib 重写：固定 `[8]string` 槽位 + n 计数，所有读取判界；`recover` 捕获 panic 转 FAIL。

### 14. `l02_ratelimiter_map_concurrent`（源 `middleware/rate_limiter_test.go:352`、`middleware/rate_limiter_test.go:456`）
- 场景：`map[string]*visitor` + token bucket，10/100 goroutine 并发 `Allow`；另测过期清理（注入假时钟，0/1/5/10 分钟）。
- GC 位置：每 1000 次 Allow GC；清理时 GC 观察被删 visitor 回收。
- 期望：固定地址的允许/拒绝序列与源表一致；清理后仅保留 A/B/D；并发下计数守恒。
- 维度：L02/L18/L05；复杂度：中；优先级：P2（清理子测试可单独作 P1）。
- stdlib 重写：`sync.Mutex` + 可注入 `timeNow`；手写浮点令牌桶，不引入 `x/time/rate`。

### 15. `l19_client_cancel_status`（源 `middleware/proxy_test.go:717`）
- 场景：handler 阻塞等 upstream；请求 context 10ms 后取消；handler 监听 `ctx.Done()` 并返回固定 499 状态。
- GC 位置：取消后 GC 释放请求/响应缓冲；upstream goroutine 退出前 `KeepAlive`。
- 期望：状态 499、body 固定、upstream 在取消后退出；二次取消无副作用。
- 维度：L02/L19；复杂度：高；优先级：P2。
- stdlib 重写：不起真实代理；直接调用 handler，用 `context.WithCancel` + channel 模拟 upstream；`httptest.NewRecorder`。

## 不可移植清单

| 测试/文件 | 不可移植原因 |
|---|---|
| `echo_test.go:943`、`echo_test.go:1017`（`:1028` 引用 `_fixture/certs/cert.pem`/`key.pem`） | 依赖 TLS 证书 fixture；partial clone 中 `_fixture/certs/` 只有 README，无证书。需要真实 TLS 握手与 listener |
| `echo_test.go:1139` `TestEcho_StartAutoTLS` | 自动 ACME/Let’s Encrypt 流程，依赖外网与域名，CoreGoLLVM 独立环境不可用 |
| `echo_test.go:1177` `TestEcho_StartH2CServer` | h2c 依赖 `golang.org/x/net/http2`，标准库无 h2c server 入口；不能单包 main 链接 |
| `echo_test.go:1493`（地址表 `:1476`–`:1479`） | 固定绑定 `127.0.0.1:1323`/`[::1]:1323`，端口冲突与 IPv6 可用性依环境；仅 `:0` 版本可移植 |
| `echo_test.go:70`、`echo_test.go:262`、`context_fs_test.go:15`、`echo_fs_test.go:16`、`group_fs_test.go:15`、`middleware/static_test.go:19` | 依赖 `_fixture/images/walle.png`、`_fixture/scripts/`、`_fixture/folder` 等文件；partial clone 中不存在。文件大小断言（如 219885）无法重建，改写须换 `fstest.MapFS` 并改断言 |
| `middleware/proxy_test.go:874`、`:915`、`:959`、`:1000` | WebSocket 代理用例依赖 `golang.org/x/net/websocket` + TLS 双端；CoreGoLLVM 下不可独立编译 |
| `middleware/proxy_test.go:28`、`:402`、`:581`、`:717` | 依赖 `httptest.NewServer` 真实 TCP upstream 与 `http.Transport` 超时/重试语义；`TestProxyRetries` 另硬编码 `127.0.0.1:27121` 作为坏目标。重写应直接调用 handler + channel 模拟 upstream，取消除端口依赖 |
| `middleware/logger_test.go:91`、`middleware/recover_test.go:64`、`middleware/request_logger_test.go:18` | 断言绑定 `labstack/gommon/log` 的 JSON/文本格式、level 类型与时间格式，独立 main 无法引入；只能提炼场景（如 `:175` 的 unsafe 转换）并用 `log`/`slog` 重写 |
| `middleware/rate_limiter_test.go:17` 起 | 直接使用 `golang.org/x/time/rate` 的 `rate.Limiter`/`Limit`；需手写令牌桶替代 |
| `middleware/util_test.go:125` | 基于随机源统计字符分布偏差（±5%），输出概率性、10 万次循环耗时；不适合作为确定性用例，除非改固定种子/固定样本 |
| `middleware/timeout_test.go:204`、`middleware/timeout_test.go:332`、`middleware/context_timeout_test.go:103` 等 | `:204` 以 1ms timer 与 handler 竞速，200/503 都是合法结果；`:332` 依赖真实 `net.Listen` server 与 `http.Server.ErrorLog`；`:103` 依赖 timer 与 `t.Parallel`。均需改为 channel 同步/直接调用 handler 才能稳定复现 |
| `echo_test.go:1556` | 断言 handler 名为完整构建路径 `github.com/labstack/echo/v4.TestEcho_OnAddRouteHandler.func1`；换模块路径/编译器会变，只能比较后缀 |
| `echo_test.go:1220` 起的 `testMethod` | 依赖 `*Echo` 的导出方法集与 `reflect.MethodByName` 字符串调度；场景可移植，但代码绑定 Echo 类型，需换成本地结构体 |

补充说明：
- 除 `binder_external_test.go` 外，所有测试均为 `package echo`，直接访问 `e.pool`、`e.maxParam`、`router.Find`、`context` 等未导出成员，不能原样编译进 `package main`，只能按场景重写。
- `github.com/stretchr/testify` 不属于标准库；所有 `assert.Equal` / `require` 需换成 `if got != want { ... }` 或小型本地辅助函数。
- `httptest.NewRequest` / `httptest.NewRecorder` 本身属于标准库，重写时可以保留；`httptest.NewServer` 也是标准库，但一旦涉及真实端口、TLS 证书、WebSocket 或代理 transport，就不再满足“输出确定/独立编译”，应优先改为直接调用 handler 或 `net.Pipe`。

## 核验记录

- 版本核验：`git tag --points-at HEAD` → `v4.13.4`；`git log -1` → `98ca08e7dd64075b858e758d6693bf9799340756`（2025-05-22）；`go.mod` → `go 1.23.0`，`module github.com/labstack/echo/v4`。
- 实际使用的 grep 关键词：`func (Test|Benchmark)`、`go func`、`goroutine`、`WaitGroup`、`sync\.`、`atomic\.`、`pool|Pool`、`context\.With`、`Timeout`、`Shutdown`、`Close\(`、`t\.Parallel`、`race`、`unsafe`、`noescape`、`reflect`、`MethodByName`、`interface`、`bind`、`Unmarshal`、`io\.Copy`、`Flush`、`\[\]byte\(`、`bytes\.Buffer`、`strconv\.`、`httptest`、`_fixture`、`go:build|+build`、`gommon|x/time|x/net|yaml`。
- 行号复核：对场景表与推荐用例中的每个 `file:line` 执行 `sed -n '<line>p' <file>`，确认该行落在对应 `func Test...`/`func Benchmark...`/关键语句上；全部引用行号 ≤ 文件总行数。
- 精读测试文件（25 个，均 ≤30）：`echo_test.go`、`context_test.go`、`context_fs_test.go`、`echo_fs_test.go`、`group_fs_test.go`、`group_test.go`、`response_test.go`、`json_test.go`、`renderer_test.go`、`router_test.go`、`bind_test.go`、`binder_test.go`、`binder_external_test.go`、`ip_test.go`、`middleware/timeout_test.go`、`middleware/context_timeout_test.go`、`middleware/rate_limiter_test.go`、`middleware/decompress_test.go`、`middleware/compress_test.go`、`middleware/body_dump_test.go`、`middleware/proxy_test.go`、`middleware/util_test.go`、`middleware/recover_test.go`、`middleware/method_override_test.go`、`middleware/extractor_test.go`。
- 规模统计：`find ... -name '*_test.go' | wc -l` = 39；`wc -l` 合计 20,566 行；`grep -c 'func Test'` 合计 408；`grep -rn 'func Benchmark'` = 35。
- 未读取文件说明：`middleware/cors_test.go`、`middleware/csrf_test.go`、`middleware/key_auth_test.go`、`middleware/static_test.go` 等仅做关键词/导入扫描，未逐段精读；其场景（CORS/CSRF/鉴权/静态文件）不在本库重点维度内。
