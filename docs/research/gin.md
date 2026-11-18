# gin v1.10.0

- 来源：`research/repos/gin`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / `README`；唯一实现文件为 `testdata/protoexample/test.pb.go`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/gin`
- 重点维度：L02 高并发、L06 反射与接口、L07 unsafe/零拷贝、L05 分配/GC、L11 编译优化、L18 写屏障、L19 netpoll/流式/关闭
- 本地核验：`git describe --tags` = `v1.10.0`；HEAD `75ccf94d`（完整 `75ccf94d605a05fe24817fc2f166f6f2959d5cea`，2024-05-07，`feat: update version constant to v1.10.0 (#3952)`）；`go.mod` 声明 `module github.com/gin-gonic/gin`、`go 1.20`
- 测试规模：38 个 `*_test.go`，合计 12,016 行；本次精读 25 个测试文件，仓内共 48 个非 `.git` 文件
- 依赖提示：`go.mod` require `sonic`、`gin-contrib/sse`、`go-playground/validator/v10`、`goccy/go-json`、`json-iterator`、`go-toml/v2`、`ugorji/go/codec`、`protobuf`、`x/net`、`yaml.v3`、`testify`。partial clone 缺少 `gin`/`binding`/`render` 的实现与 `internal/json`，全部测试无法直接编译；测试还大量调用未导出的 `mapping`/`mappingByPtr`/`responseWriter` 等，只能提炼场景后用 stdlib 重写
- 构建标签：`binding/binding_msgpack_test.go:5`、`binding/msgpack_test.go:5`、`render/render_msgpack_test.go:5` 为 `//go:build !nomsgpack`；`context_1.18_test.go:5` 为 `!go1.19`、`context_1.19_test.go:5` 为 `go1.19`。测试文件无 GOOS/GOARCH 标签，但 `gin_integration_test.go` 的 Unix socket / fd 用例含平台语义

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| `context_test.go:149` | `TestContextReset`：池化 Context 复用，手动设置 index/Writer/Params/Error/Keys 后 `reset()`，断言 Keys/Accepted/Errors/Params 清空、index=-1、Writer 指回内嵌 writermem（池复用语义见 `:41` 注释） | L05/L18/L11 | 中 | 自建 context 结构 + `sync.Pool`；请求结束 reset 全字段再 Put；GC 后取回验证无旧值残留 |
| `context_test.go:320` | `TestContextCopy`：Copy 后 handlers 与 writermem.ResponseWriter 为 nil，Request/Params/Keys/engine/fullPath 与源一致；修改副本 Keys 不影响源 | L06/L18/L02 | 中 | struct 拷贝 + Keys map/Params slice 深拷贝；源对象 reset 后副本仍可读；GC 插在 Copy 与读取之间 |
| `context_test.go:2401` | `TestRaceParamsContextCopy`：两次请求分别命中 `/:name`，handler 内 `go c.Copy()`，延迟后断言副本 Param 仍是各自请求的值 | L02/L18/L06 | 中 | 两轮请求共享 router，每次复制 params slice；后台 goroutine 读取；有界轮次 + GC |
| `context_test.go:2422` | `TestContextWithKeysMutex`：零值 Context 上 Set/Get 自动初始化 keys，读写路径不 panic | L06/L18 | 低 | 零值 map 惰性初始化 + `sync.RWMutex`；未命中返回 nil |
| `context_test.go:2352` | `TestContextStream`：Stream 循环调用回调写 `test`，回调返回 false 停止，最终 body 为 `testtest` | L19/L05 | 中 | `for` 循环 + `io.Writer`；CloseNotify 用 chan；客户端断开时 select 退出 |
| `context_test.go:2371` | `TestContextStreamWithClientGone`：回调内关闭 CloseNotify 通道，Stream 下一轮检测到客户端离开，只写一次 `test` | L19/L02 | 中 | 自定义 ResponseWriter 持 chan；关闭后循环退出；GC + goroutine 计数 |
| `context_test.go:978` | `TestContextRenderSSE`：连续 `SSEvent`/`Render` 输出 `event:`/`id:`/`data:` 帧，map 数据经 JSON 序列化 | L19/L16/L05 | 低 | 手写 SSE 帧格式化 + `encoding/json`；写入 `bytes.Buffer`；GC 后逐字节校验 |
| `context_test.go:1901` | `TestContextShouldBindBodyWith`：首次绑定后缓存原始 body，二次以不同 binding 再绑定；类型不匹配返回错误且目标保持零值 | L05/L06/L13 | 中 | `io.ReadAll` 缓存 `[]byte`；`encoding/json`/`encoding/xml` 两次解码；GC 后缓存仍有效 |
| `context_test.go:1456` | `TestContextClientIP`：`X-Forwarded-For` / `X-Real-IP` / `X-Appengine-Remote-Addr` / RemoteAddr 与受信 CIDR 组合，含 IPv6、非法 IP、hostname 解析失败 | L06/L19 | 中 | `net.ParseIP` + `net.ParseCIDR` + 链式信任回溯；只保留字面 IP 以保证确定性 |
| `context_test.go:2587` | `TestContextCopyShouldNotCancel`：请求 handler 内 Copy 后起 goroutine，等响应结束再用副本作 `context.Context` 发 HTTP 请求，Done/Err 不应被原请求取消 | L02/L19/L18 | 高 | `httptest.NewServer` 或 net.Pipe；副本实现 `Context` 且 Done 为 nil；服务关闭后后台请求仍成功 |
| `context_test.go:2686` | `TestInterceptedHeader`：中间件用嵌入 ResponseWriter 接口的结构体替换 `c.Writer`，转发时删除 X-Test；WriteHeaderNow 后头已冻结 | L06/L18 | 中 | 自定义 wrapper 实现 `http.ResponseWriter` 并转发；断言头冻结时机 |
| `context_test.go:2296` | `TestContextRenderDataFromReader`：从 `strings.Reader` 流式写 body，设置 Content-Length/Content-Type/附加头 | L05/L19 | 低 | `io.Copy` + Header 设置；大 body 分段读写并 GC |
| `gin_test.go:538` | `TestEngineHandleContextManyReEntries`：路由 `/:count` 内改写 URL 并递归 `HandleContext(c)` 1 万次，每次写 1 字节，中间件/handler 的 atomic 计数均为 10000 | L02/L03/L04 | 高 | 自写迷你 router 递归分发；深栈下沉；GC 插在递归间隙；校验字节数与计数 |
| `gin_integration_test.go:369` | `TestConcurrentHandleContext`：200 goroutine 同时请求 `/`，handler 把 URL 改成 `/example` 再 HandleContext，全部应返回 200 `it worked` | L02/L06 | 中 | 直接 `ServeHTTP` + `httptest.NewRecorder`；每 goroutine 独立 request/recorder；有界迭代 + GC |
| `gin_integration_test.go:415` | `TestTreeRunDynamicRouting`：35+ 条含 `:param`/`*catchall` 的动态路由注册后逐条请求，校验命中结果 | L13/L06/L02 | 中 | 手写分段/前缀匹配表；表驱动路径→期望；每轮查找后 GC |
| `tree_test.go:101` | `TestTreeAddAndGet`：11 条含 Unicode 的静态路由插入 radix 树后逐条查找，含 key 不匹配/无子节点返回 nil | L13/L06 | 低 | 自写 radix/前缀树；插入 + 查找表驱动 |
| `tree_test.go:138` | `TestTreeWildcard`：`:param`、`*catchall` 与静态段混排的匹配、参数提取与节点优先级 | L13/L06/L05 | 中 | 分段树 + 优先级计数 + Params 组装；表驱动大量路由 |
| `tree_test.go:323` | `TestUnescapeParameters`：路径参数含 `%2F`、`+`、`%20`、Unicode 等转义，在 unescape=true 下比较解码结果 | L13/L06 | 低 | `url.PathUnescape`/`QueryUnescape` 对照；表驱动 |
| `tree_test.go:909` | `TestTreeExpandParamsCapacity`：Params 以 cap=0 传入 `getValue`，`:path` 与 `/*path` 都必须扩容并返回 1 个参数 | L05/L13 | 低 | `make([]Param,0)` 传入查找函数；append 扩容；GC 后校验 |
| `path_test.go:79` | `TestPathCleanMallocs`：cleanPath 对 38 条路径 `AllocsPerRun=0`；仅在 `GOMAXPROCS=1` 且非 `-short` 时运行 | L11/L05 | 低 | 自写 cleanPath 复用栈缓冲；`testing.AllocsPerRun` 断言 0；固定 GOMAXPROCS 保证确定性 |
| `path_test.go:126` | `TestPathCleanLong`：长度 1..1234 的路径 × 4 种形态（无前导斜杠/重复斜杠/`..` 回退）全部清理正确 | L05/L13 | 中 | `strings.Builder` 或复用 buffer；长路径循环 + GC |
| `routes_test.go:144` | `TestRouteRedirectTrailingSlash`：尾斜杠重定向 301/307，含 `X-Forwarded-Prefix` 恶意/异常输入清理 | L13/L06 | 中 | 自写 cleanPath + Location 组装；表驱动；无需服务器 |
| `routes_test.go:679` | `TestRouteRawPath`：UseRawPath 时 `%2F` 保留在参数中并解码为 `Some/Other/Project` | L13/L06 | 低 | `url.PathUnescape` 处理参数段；对照关闭解码 |
| `routes_test.go:730` | `TestRouteContextHoldsFullPath`：每个 handler 的 FullPath 等于注册模板；404 时为空 | L13/L06 | 低 | 匹配结果携带路由模板字符串 |
| `routergroup_test.go:168` | `TestRouterGroupPipeline`：群组/子群组继承 handlers，IRoutes 各注册 API 返回自身，可链式调用 | L06 | 低 | 接口 + 方法链；断言 handlers 长度与 BasePath |
| `gin_test.go:742` | `TestCustomUnmarshalStruct`：自定义类型实现 `UnmarshalParam` 接口，BindQuery 调用自定义解码 | L06/L05 | 低 | 定义 `UnmarshalParam(string) error`；反射检测接口并调用 |
| `internal/bytesconv/bytesconv_test.go:28` | `TestBytesToString`：100 次随机 1KB `[]byte` 转 string 与 `string(b)` 比较；实现为 unsafe 零拷贝（实现不在 clone 内） | L07/L13/L05 | 低 | `unsafe.String(unsafe.SliceData(b), len)`；与 copy 版对照；改写 b 验证别名 + KeepAlive |
| `internal/bytesconv/bytesconv_test.go:66` | `TestStringToBytes`：100 个随机 64 字符 string 转 `[]byte` 与 `[]byte(s)` 比较 | L07/L13 | 低 | `unsafe.Slice(unsafe.StringData(s), len)`；仅只读验证，不写只读内存 |
| `response_writer_test.go:39` | `TestResponseWriterReset`：reset 后 size=-1、status=200、恢复底层 writer，Size/Status/Written 归零，wrapper 可复用 | L06/L18/L05 | 低 | 自写 responseWriter 状态机 + reset；池化复用 + GC |
| `response_writer_test.go:107` | `TestResponseWriterHijack`：底层不支持 Hijack/CloseNotify 时 panic，Written 置 true，Flush 为 no-op | L06/L19 | 中 | 类型断言 `http.Hijacker`/`http.CloseNotifier`；失败路径 panic 语义 |
| `response_writer_test.go:126` | `TestResponseWriterFlush`：通过真实 `httptest.Server` 让 wrapper Flush 500 状态，客户端应看到 500 | L19/L06 | 中 | httptest/net.Pipe；先 WriteHeader 再 Flush；校验客户端状态 |
| `middleware_test.go:159` | `TestMiddlewareAbort`：中间件链签名 A→C→AbortWithStatus(401)→Next→D，最终只输出 ACD，业务 handler 不执行 | L02/L06 | 低 | handlers slice + index + abort 标志；断言执行顺序 |
| `middleware_test.go:229` | `TestMiddlewareWrite`：String/XML/JSON/SSE 依次写同一响应（状态 400），body 按中间件顺序拼接 | L06/L13 | 低 | 自写 ResponseWriter 记录首次状态；顺序写 buffer |
| `binding/form_mapping_test.go:19` | `TestMappingBaseTypes`：反射对 20 组标量/指针字段按 form 值赋值，含零值与 file 类型 | L06/L05 | 中 | 反射遍历字段 + `strconv` 解析；表驱动；批量分配后 GC |
| `binding/form_mapping_test.go:222` | `TestMappingSlice`：form tag `default=9` 与多值 slice 解析，非法值报错 | L06/L05 | 低 | `reflect.MakeSlice` + 逐值 ParseInt |
| `binding/form_mapping_test.go:277` | `TestMappingPtrField`：`[]*struct` 从 0/1/2 个 JSON 字符串表单值反序列化，逐项分配指针 | L06/L05/L18 | 中 | `encoding/json` 解每项 + 指针切片；GC 校验元素存活 |
| `binding/form_mapping_test.go:320` | `TestMappingIgnoredCircularRef`：自引用 struct 字段带 `form:"-"` 时必须跳过，不得无限递归 | L06/L04 | 中 | 反射递归 + 标签过滤 + 深度上限 |
| `binding/validate_test.go:111` | `TestValidateNoValidationValues`：validator 遍历含嵌入结构、切片、指针、接口、map 的大结构体，无 tag 时不报错且不改原值 | L06/L05 | 中 | 自写反射校验器，无规则直接返回；大对象 + GC |
| `binding/default_validator_test.go:49` | `TestDefaultValidator`：`max=8`/`gt=0` 对 struct、`*struct`、`[]struct`、`[]*struct`、`*[]struct` 等 20 种形态判定 | L06 | 中 | 反射解析 tag 逐字段校验；表驱动 |
| `binding/binding_test.go:651` | `TestBindingFormFilesMultipart`：multipart 表单文件与字段一起绑定，`FileHeader.Open` 内容需与 `os.Open("form.go")` 一致 | L05/L19 | 中 | `mime/multipart` + `io.ReadAll`；该测试依赖实现源码 `form.go`，partial clone 不可直接跑 |
| `binding/binding_test.go:771` | `TestHeaderBinding`：header tag 把 `limit:1000` 解析为 int；map 字段遇到非法 JSON 报错 | L06/L05 | 低 | `http.Header.Get` + 反射；表驱动 |
| `render/render_test.go:27` | `TestRenderJSON`：map 含 HTML 字符时输出 `\u003c` 转义，Content-Type 固定；chan 输入返回错误 | L16/L06/L05 | 低 | `encoding/json.Marshal` + `SetEscapeHTML`；错误路径 |
| `render/render_test.go:456` | `TestRenderHTMLTemplate`：`html/template` 执行命名模板，输出 `Hello alexandernyquist` | L16/L06 | 低 | `html/template` 直接执行到 buffer |
| `render/render_test.go:536` | `TestRenderReader`：Reader 渲染设置 Content-Length/Content-Type/自定义头；负长度时不写 Content-Length | L05/L19 | 低 | `io.Copy` + Header 设置；构造负长度变体 |
| `auth_test.go:48` | `TestBasicAuthSearchCredential`：预生成 base64 Basic 凭据表，线性搜索命中/空串/尾随空格失败 | L13/L06 | 低 | `encoding/base64` + 切片线性搜索；固定账号表 |
| `errors_test.go:115` | `TestErrorUnwrap`：两层 `fmt.Errorf("%w")` 包裹自定义 Error，`errors.Is`/`As` 能取到内层错误 | L06 | 低 | 自定义 error 实现 `Unwrap`；`errors.Is`/`As` 验证 |
| `githubapi_test.go:349` | `TestRaceContextCopy`：同一请求的两个 `Context.Copy()` 交给两个 goroutine 无限 Set/Value，要求无 panic/race | L02/L18/L06 | 高 | 有界轮次 + 两份独立 keys map + RWMutex；GC 穿插 |
| `logger_test.go:22` | `TestLogger`：LoggerWithWriter 记录方法/路径/状态/查询串，覆盖 7 种方法与 404 | L16/L05 | 低 | 自写访问日志中间件写入 `strings.Builder`；去掉时间/颜色以保证确定性 |
| `debug_test.go:35` | `TestDebugPrint`：按 Debug/Release/Test 模式决定 debugPrint 是否输出并格式化参数 | L06/L11 | 低 | 全局 mode 变量 + `fmt.Fprintf` 到 buffer |

## 推荐收录用例（15 条）

以下用例均按单包 `package main`、仅标准库、固定输入/固定输出实现；失败打印 `FAIL: ...` 并以退出码 1 结束。

### 1. `l05_context_pool_reuse`（源 `context_test.go:149`，池语义注释 `:41`）
- 场景与做法：自建与 gin 等价的请求上下文（index、Writer 包装、Params、Keys、Errors、handlers），每轮请求结束后 `reset()` 再放入 `sync.Pool`；下一轮取出并断言全部字段已清空、Writer 指向内嵌状态机。
- 维度/复杂度/优先级：L05/L18/L11 · 中 · P0。
- stdlib 重写：`sync.Pool` + 显式 reset；每轮分配 1MB 垃圾并 `runtime.GC()`，GC 后取回对象仍须干净；为确定性另保留对象切片做 reset 语义断言，不依赖 Pool 一定命中。输出仅打印固定轮次与字段校验结果。

### 2. `l18_context_copy_async`（源 `context_test.go:320`、`context_test.go:2401`）
- 场景与做法：handler 内 `Copy()`（深拷贝 Keys map、Params slice），把副本交给后台 goroutine；原请求结束后源上下文 reset/放回池；goroutine 在多次 GC 后读取副本的 Param/Keys，值必须保持。
- 维度/复杂度/优先级：L18/L02/L06 · 中 · P1。
- stdlib 重写：两个等价的上下文结构，源对象 reset 并清空字段；副本独立持有 map/slice。GC 放在 reset 之后、goroutine 读取之前与之后各一次，验证老对象到副本引用的存活；用 WaitGroup 收敛。

### 3. `l02_context_copy_keys_race`（源 `githubapi_test.go:349`、`context_test.go:2401`）
- 场景与做法：同一请求创建两份 Copy，各自 ratelimit 下并发 Set/Get 固定 key 集合；每份副本独立加锁，不得共享 map、不得 panic/丢 key。原测试为无限循环，重写改为固定轮次。
- 维度/复杂度/优先级：L02/L18/L06 · 中 · P1。
- stdlib 重写：每副本 `map[string]any` + `sync.RWMutex`；32 goroutine × 5000 轮，主 goroutine 每 100 轮 `runtime.GC()`；最后校验每个 key 的计数/末值确定。

### 4. `l02_concurrent_handle_context`（源 `gin_integration_test.go:369`）
- 场景与做法：200 个 goroutine 并发走同一路由，handler 改写请求路径后再次分发（模拟 gin `HandleContext`），每条请求应返回固定 body 与 200；并发查找只读路由树，不在服务期 addRoute。
- 维度/复杂度/优先级：L02/L06 · 中 · P1。
- stdlib 重写：自写只读路由表 + `http.Handler`；每 goroutine 独立 `httptest.NewRequest`/`Recorder`，直接 `ServeHTTP`，不建监听器；每 50 轮 GC 一次；断言总成功数。

### 5. `l04_handle_context_reentry`（源 `gin_test.go:538`）
- 场景与做法：递归重入 10000 层，每层改写 URL 并再次分发、写 1 字节；中间件与业务 handler 各做一次 atomic 计数；校验响应长度 10000、两个计数均为 10000。
- 维度/复杂度/优先级：L04/L03/L02 · 高 · P1。
- stdlib 重写：手写分发循环/递归（每层调用链保持活跃以触发 morestack/栈复制）；atomic 计数；每 1000 层 `runtime.GC()`；固定递归深度，输出确定。

### 6. `l11_path_clean_zero_alloc`（源 `path_test.go:79`）
- 场景与做法：实现 `cleanPath`（补前导斜杠、合并重复斜杠、消解 `.`/`..`），对 38 条固定路径断言结果，并以 `testing.AllocsPerRun(100, ...)` 断言 0 分配。
- 维度/复杂度/优先级：L11/L05 · 低 · P0。
- stdlib 重写：只在输入需要修改时才分配输出，否则原样返回；用 `testing.AllocsPerRun`（stdlib，可被 main 调用）测量；测量前后 `runtime.GC()`；固定 `GOMAXPROCS=1` 保证可重复。

### 7. `l07_bytesconv_unsafe_alias`（源 `internal/bytesconv/bytesconv_test.go:28`、`:66`）
- 场景与做法：用 `unsafe.String`/`unsafe.Slice` 实现 `[]byte↔string` 零拷贝转换；与 copy 版逐字节比较；改写原 `[]byte` 后零拷贝 string 应观察到变化，copy 版不变。
- 维度/复杂度/优先级：L07/L13/L18 · 中 · P1。
- stdlib 重写：`unsafe.String(unsafe.SliceData(b), len(b))`、`unsafe.Slice(unsafe.StringData(s), len(s))`；读取期间 `runtime.KeepAlive` 保活；GC 放在转换后与读取前，验证指针位图/对象存活；StringToBytes 结果只读，避免写只读内存。输出只含固定字符串比较结果。

### 8. `l13_radix_wildcard_params`（源 `tree_test.go:138`、`tree_test.go:909`）
- 场景与做法：注册 40+ 条静态/`:param`/`*catchall` 混排路由，依次查找；每次以 cap=0 的 Params 切片接收结果，校验参数名/值/个数与命中 handler；覆盖节点优先级与 Params 扩容。
- 维度/复杂度/优先级：L13/L05/L06 · 中 · P0。
- stdlib 重写：分段前缀树 + 递归匹配；Params 用 `append` 扩容；每次查找后 GC；表驱动输入输出，避免 map 遍历顺序参与判定。

### 9. `l06_form_reflect_mapping`（源 `binding/form_mapping_test.go:19`、`:277`）
- 场景与做法：`reflect.New/Elem/Field` 遍历结构体字段，按 `form`/`uri` tag 解析 int/uint/bool/float/string/指针/slice/`[]*struct`；含零值、默认值、非法值错误路径。
- 维度/复杂度/优先级：L06/L05/L18 · 中 · P1。
- stdlib 重写：静态定义目标 struct，反射 + `strconv`/`encoding/json` 解析；批量创建 1 万个对象后 GC，校验指针切片元素仍存活；表驱动输出确定。

### 10. `l06_form_reflect_recursive`（源 `binding/form_mapping_test.go:320`）
- 场景与做法：自引用结构体（字段 `S *S` 带 `form:"-"` tag），反射映射必须识别 `-` 并跳过，否则会无限递归；同时覆盖未导出字段跳过与未知类型报错。
- 维度/复杂度/优先级：L06/L04 · 中 · P1。
- stdlib 重写：递归反射 + tag 过滤 + 最大深度保护；构造带环对象后映射并发 GC；断言不 panic、字段保持零值。

### 11. `l06_validator_struct_tags`（源 `binding/default_validator_test.go:49`、`binding/validate_test.go:111`）
- 场景与做法：手写 `required`/`max=8`/`gt=0` 规则，对 struct、`*struct`、`[]struct`、`[]*struct`、`*[]struct`、含嵌入/接口/map/slice 的大结构体做校验；无规则对象必须原样通过。
- 维度/复杂度/优先级：L06/L05 · 中 · P1。
- stdlib 重写：反射遍历 tag 并解析参数；批量校验 1 万次，GC 穿插；只输出通过/失败计数与固定错误文本，不依赖 validator 库的错误格式。

### 12. `l06_writer_interface_chain`（源 `response_writer_test.go:33`、`:107`、`:174`、`context_test.go:2686`）
- 场景与做法：构造 response writer 包装链，分别实现 `http.ResponseWriter`、`http.Hijacker`、`http.Flusher`、`http.CloseNotifier`、`http.Pusher`；中间件替换 Writer 并转发；断言类型断言结果、首次状态码、头冻结与 reset 复用。
- 维度/复杂度/优先级：L06/L18/L19 · 中 · P1。
- stdlib 重写：接口组合 + 类型断言；不支持 Hijack/CloseNotify 时 panic；wrapper 池化 reset 后复用；GC 后检查字段不残留旧状态。

### 13. `l05_bind_body_cache`（源 `context_test.go:1901`）
- 场景与做法：首次读取并缓存请求 body 字节，随后用 JSON 与 XML 两种方式重复绑定；类型不匹配时返回错误且目标保持零值；缓存 body 在多次 GC 后仍可重读。
- 维度/复杂度/优先级：L05/L06/L18 · 中 · P1。
- stdlib 重写：`io.ReadAll` + context 内保存 `[]byte`；`encoding/json`/`encoding/xml` 两次解码；GC 放在两次绑定之间；固定 body 与错误文本。

### 14. `l19_stream_client_gone`（源 `context_test.go:2352`、`:2371`、`:978`）
- 场景与做法：自定义 ResponseWriter 带 CloseNotify 通道；handler 循环写固定 SSE 帧，写满 N 帧后关闭通道模拟客户端离开；Stream 必须停止并关闭 goroutine。
- 维度/复杂度/优先级：L19/L02/L18 · 中 · P1。
- stdlib 重写：`chan struct{}` + `select` 控制循环；用 channel 精确编排“写第 N 帧→关闭”，不用 sleep；goroutine 计数 + WaitGroup；GC 放在流结束前后各一次；输出帧内容固定。

### 15. `l19_graceful_shutdown_inflight`（源 `gin_integration_test.go:322`、`context_test.go:2587`；gin 本身无 `Shutdown` 测试，属场景补全）
- 场景与做法：`http.Server` 监听 `127.0.0.1:0`，一个 handler 阻塞在 channel 上并持续写流；发起请求后调用 `Shutdown(ctx)`，再放行 handler；`Shutdown` 应等 handler 完成、响应完整，之后新连接被拒绝。
- 维度/复杂度/优先级：L19/L02 · 高 · P2。
- stdlib 重写：`net.Listen("tcp","127.0.0.1:0")` + `http.Server.Shutdown`；channel 确定性地控制“进入 handler/开始 Shutdown/释放 handler”；1s 超时保护；GC 放在 Shutdown 前后；只打印固定状态与错误计数。

## 不可移植清单

| 测试/文件 | 不可移植原因 |
|---|---|
| `gin_integration_test.go:155` `TestRunTLS` | 真实 TLS 监听 8443 + `testdata/certificate/*.pem`，依赖端口、证书与 TLS 握手时序；只能借鉴“TLS server 生命周期”场景 |
| `gin_integration_test.go:171` `TestPusher` | 通过 `RunTLS` 启动 HTTP/2 并用 `http.Pusher` 推送静态资源；依赖 TLS/HTTP2、`golang.org/x/net/http2` 与支持 push 的客户端 |
| `gin_test.go:84` `TestH2c` | 裸 TCP + `x/net/http2` h2c 升级，依赖协议协商与真实连接 |
| `gin_integration_test.go:244` `TestUnixSocket` | Unix domain socket + 固定临时路径 + `time.Sleep`，平台与文件系统依赖 |
| `gin_integration_test.go:277` `TestFileDescriptor` | `listener.File()`/`RunFd`，Windows 分支明确不支持；依赖进程 fd 与平台 syscall |
| `gin_integration_test.go:65`、`:230`、`:322` | `Run`/`RunWithPort` 使用固定端口，`RunListener` 使用真实监听 + 后台 server goroutine + 5ms sleep；整体非确定性，改写需用 `127.0.0.1:0` 并自行 `Shutdown` |
| `context_test.go:995`、`context_test.go:1009` | `File`/`FileFromFS` 直接读 `./gin.go`；partial clone 无实现源码，断言内容不存在 |
| `routes_test.go:449` `TestRouteStaticListingDir` | 目录列举断言包含 `gin.go`，partial clone 无实现源码 |
| `binding/binding_test.go:651` | 依赖 `os.Open("form.go")` 的实现源码文件，partial clone 不存在 |
| `context_test.go:1093`、`render/render_test.go:326`、`binding/binding_test.go:707` | protobuf 绑定/渲染依赖 `google.golang.org/protobuf` 与生成类型 `testdata/protoexample/test.pb.go`，不是标准库可等价重写 |
| `binding/yaml_test.go:14`、`binding/toml_test.go:14`、`binding/msgpack_test.go:18`、`binding/binding_msgpack_test.go:17`、`render/render_msgpack_test.go:21` | 依赖 yaml.v3 / go-toml / ugorji codec；仅可换 `encoding/json`/`encoding/xml` 重写同类“多格式绑定”场景，格式语义不可移植 |
| `binding/validate_test.go:204`、`:233` | 直接使用 go-playground/validator 的 `RegisterStructValidation`/`RegisterValidation` 引擎 API；规则可用手写反射复刻，但 API/错误格式不可移植 |
| `logger_test.go:150`、`:182`、`:234` | 日志格式含时间戳、延迟、颜色/终端判断；输出不确定且依赖 `DefaultWriter` 全局状态 |
| `debug_test.go:99`、`:153` | `:99` 依据 `runtime.Version()` 与 gin 最低版本分支输出不同 WARNING，`:153` 解析版本字符串；结果随 Go 版本/实现变化 |
| `context_test.go:1456` | 受信代理包含主机名 `foo`/`bar`，会触发 DNS 解析，环境相关；字面 IP/CIDR 部分可移植 |
| `githubapi_test.go:349` | 两个 goroutine 无限 `for` 循环 Set/Value 且无同步退出；只能按固定轮次重写，不能原样收进用例 |
| `response_writer_test.go:126`、`context_test.go:2587` | 依赖真实连接语义（Flush 到达客户端、请求结束后 context 不被取消）；不能改为直接调 handler，需 stdlib 的 `httptest.NewServer` 或 net.Pipe 承载 |
| `testdata/protoexample/test.pb.go` | 唯一实现文件，属于 protobuf 生成代码；不可作为 gin 行为参考，也不应被复制进用例 |

### httptest 甄别

- 可直接改为直接调用 handler / `ServeHTTP`：`httptest.NewRecorder` 类测试（context 渲染、binding、render、routes、middleware、response writer 状态机），以及 `gin_integration_test.go:369`、`:415`（原测试只用状态码/body，无需真实网络）。
- 需要真实连接语义，可用 stdlib 改写但不可直接调 handler：`response_writer_test.go:126`（Flush 到达客户端）、`context_test.go:2587`（请求结束后 Context 仍可用）、`context_test.go:2352`/`:2371`（CloseNotify；用自定义 writer 的 channel 即可，无需服务器）。
- 不可移植：TLS/HTTP2/Pusher/h2c/Unix socket/fd 类（见上表），以及依赖 DNS 或固定端口的用例。

## 核验记录

- 实际 grep 关键词/命令：
  - `grep -nE '^func (Test|Benchmark)' <全部 *_test.go>`（建立 38 个文件的函数/行号索引）
  - `grep -nE 'sync\.Pool|pool|reuse|Reset\(\)|CloseNotifier|Stream|ClientGone|goroutine|go func|WaitGroup|race|Mutex|RWMutex|atomic|context\.|Cancel|Deadline|Done\(\)' context_test.go`
  - `grep -nE 'httptest|NewServer|NewRecorder|net\.Pipe|TLS|Listener|Serve\(|Shutdown|Close\(|Hijack|Flush|Pusher|http2|h2c|unix|fd|FileDescriptor' gin_integration_test.go gin_test.go response_writer_test.go`
  - `grep -rnE 'concurrent|goroutine|go func|sync\.|atomic\.|race|Mutex|Pool|reuse' --include='*_test.go' .`
  - `grep -nE '^//go:build|^// \+build' *.go binding/*.go render/*.go internal/**/*.go`
  - 对表中每个 `文件:行` 执行 `sed -n '<line>p' <file>` 复核，确认行号落在对应 Test/Benchmark 函数或关键语句上，且 `line ≤ wc -l`。
- 精读文件（25 个）：`context_test.go`、`gin_test.go`、`gin_integration_test.go`、`tree_test.go`、`path_test.go`、`internal/bytesconv/bytesconv_test.go`、`response_writer_test.go`、`middleware_test.go`、`binding/binding_test.go`、`binding/form_mapping_test.go`、`binding/validate_test.go`、`binding/default_validator_test.go`、`render/render_test.go`、`auth_test.go`、`routes_test.go`、`routergroup_test.go`、`debug_test.go`、`logger_test.go`、`errors_test.go`、`mode_test.go`、`utils_test.go`、`recovery_test.go`、`githubapi_test.go`、`context_1.18_test.go`、`context_1.19_test.go`。
- 规模复核：`find ... -name '*_test.go' | wc -l` = 38；`wc -l` 合计 12,016 行；未超出 30 个精读文件上限。
