# caddy v2.10.0

- 来源：`research/repos/caddy`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / `README`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/caddy`
- 本地核验：`git describe` = `v2.10.0`，HEAD `fb22a26b`（2025-04-18，`caddytls: Allow missing ECH meta file`）
- 模块：`github.com/caddyserver/caddy/v2`，`go 1.24`
- 范围：仓库内 73 个 `*_test.go`。先按 `go func|chan|select|sync|atomic|Once|NumGoroutine|Shutdown|Cancel|timeout|reflect|LoadModule|RegisterModule|Pool|Clone` 筛选，再精读 `admin_test.go`、`modules_test.go`、`context_test.go`、`replacer_test.go`、`listeners_test.go`、`modules/caddyhttp/{server,responsewriter,metrics,matchers,headers}`、`reverseproxy/selectionpolicies`、`templates`、`caddyfile/lexer`、`caddytest/integration/stream_test.go` 等约 24 个文件；文件:行均相对本仓根目录。
- 说明：Caddy 的 L02/L04 密度偏低，单元测试多为表驱动解析/配置；并发压力主要集中在 integration（真实 HTTP/TLS）与 `Load`/模块注册。因此场景表把“可提炼的运行时机制”与“依赖真实服务的集成场景”分开，推荐项优先纯 stdlib 可复现的部分。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| `modules_test.go:22` | 全局模块注册表（`map[string]ModuleInfo`）按命名空间前缀 `GetModules("a.b")` 返回子模块，`modulesMu` 保护；`ModuleID` 拆分 namespace/name（`:87`） | L06/L12/L02 | 低 | 包级 `map[string]info` + `sync.RWMutex`；init 注册若干类型，前缀查询 + 排序；并发读写压测 |
| `admin_test.go:51` | `/config/` JSON 树上的 POST/PUT/PATCH/DELETE：新增/替换 map 键、数组下标、`/list/...` 批量追加；失败路径要求确定错误 | L06/L13 | 中 | `any` 树 + type switch（`map[string]any`/`[]any`/标量）；路径解析器与递归 `...` 展开 |
| `admin_test.go:147` | 100 个 goroutine 并发 `Load(testCfg, true)`；每个 Load 都做 JSON 解码、模块 provisioning、配置替换；注释明确为 `-race` 场景 | L02/L12/L06 | 高 | 自写配置结构 + `sync.RWMutex` 保护的当前配置指针；100 goroutine 反复 decode/validate/provision，GC 穿插 |
| `admin_test.go:174` | 配置 ETag：读配置算 hash，错误 ETag 返回 412，正确 ETag 才允许变更 | L13/L06 | 低 | `sha256` 对规范化 JSON 计算 ETag；`compare-and-swap` + 锁保护 |
| `context_test.go:22` | `Context.LoadModule` 示例（无 `// Output:`，编译但不执行）通过结构体字段 tag（`caddy:"namespace=... inline_key=name"`）反射定位 `json.RawMessage`，实例化模块并断言到 `io.Writer`；数组（`:54`）/map（`:87`）两种容器形态 | L06/L12 | 中 | `reflect.Type.Field` 读 tag；按 `namespace.inline_key` 查注册表；构造对象后 `.(io.Writer)` 断言，失败返回错误 |
| `replacer_test.go:26` | 占位符 `{...}` 解析：转义 `\{`、嵌套花括号、未闭合、二进制字节；`ReplaceAll` 对未知 key 用 empty 值；`mapMutex` 为 RWMutex（`:242`） | L13/L06/L04 | 中 | 手写扫描器：状态机处理转义/嵌套；静态 `map[string]any` + provider 函数链；构造 10k 层嵌套做栈压力 |
| `replacer_test.go:240` | 两个 `ReplacerFunc` provider 串联查询，支持 `any` 返回值（字符串、空值）与未知 key 原样保留 | L06/L02 | 低 | `[]func(key string)(any,bool)` + RWMutex；闭包/函数值调用，多 goroutine 并发 Get |
| `replacer_test.go:347` | `Map()` 追加 provider 并校验函数指针身份；`Delete` 从静态 map 删除（`:313`） | L06 | 低 | 函数切片 + map 删除；用 `reflect.ValueOf(fn).Pointer()` 校验同一性 |
| `modules/caddyhttp/responsewriter_test.go:47` | `ResponseWriterWrapper` 包装 `http.ResponseWriter`，通过接口断言探测底层是否实现 `io.ReaderFrom`；有则走 `ReadFrom`，无则回退 `io.Copy` | L06 | 中 | 自定义 `ResponseWriter` 接口；`w.(io.ReaderFrom)` 双分支；计数器断言调用路径 |
| `modules/caddyhttp/metrics_test.go:30` | `sync.Once` 延迟初始化指标；instrumented handler 前后 in-flight gauge 必须 1→0，错误路径也要归零（`:205` 为 per-host 变体） | L02/L06 | 中 | `sync.Once` + atomic 计数器（模拟 gauge）；handler 链 + defer 保证归零 |
| `modules/caddyhttp/reverseproxy/selectionpolicies_test.go:36` | RoundRobin 选择：计数器递增、跳过 unhealthy/full 上游；`setHealthy`/`countRequest` 为原子操作。`WeightedRoundRobin`（`:77`）、`LeastConn`（`:186`） | L02/L13 | 中 | `atomic.Uint64` 轮转 + `atomic.Bool` 健康位 + `atomic.Int64` 在途数；并发 Select 校验分布与跳过策略 |
| `modules/caddyhttp/templates/tplcontext_test.go:190` | 模板嵌套 include：父模板 include 子模板、变量传递、错误传播；缓冲区来自 `sync.Pool`（`bufPool.Get/Put`） | L05/L04/L16 | 中 | `text/template` + 自定义 include 函数；`sync.Pool` 复用 `bytes.Buffer`；构造递归 include 链测栈 |
| `caddyconfig/caddyfile/lexer_test.go:21` | Caddyfile 词法分析：引号/转义/heredoc/注释/行号，数百条 token 断言 | L13/L16/L05 | 中 | 手写 lexer 状态机；token 结构 + 位置；长输入循环分配，GC 插在每轮 token 化之间 |
| `listeners_test.go:178` | 网络地址解析：`tcp/host:port`、端口范围、unix socket 权限位、`Expand` 展开；`ParseNetworkAddressWithDefaults`（`:305`） | L13 | 低 | `net.SplitHostPort` + 自定义 range 解析；表驱动覆盖非法输入 |
| `caddyconfig/httpcaddyfile/httptype_test.go:9` | Caddyfile matcher 语法（host/path/header/expression）转 JSON 路由配置；`TestSpecificity`（`:77`）排序 | L06/L13 | 中 | 手写 token→结构体映射；用 `encoding/json` 输出并深比较 |
| `modules/caddyhttp/matchers_test.go:1045` | `MatchNot`：对一组 MatcherSet 取反，空集合恒 true；Matcher 为接口，`Provision`/`Match` 两段式 | L06/L13 | 低 | 定义 `Matcher interface{ Match(*http.Request) bool }`；And/Not 组合器 + 空集合语义 |
| `modules/caddyhttp/headers/headers_test.go:29` | 请求/响应头 Add/Set/Delete，支持 `*-suffix`、`prefix-*`、`*_*` 通配删除 | L05/L13 | 中 | `http.Header` 遍历 + 通配匹配；大量 header 反复增删，GC 后校验 |
| `modules/caddyhttp/fileserver/matcher_test.go:31` | 文件 matcher：try_files、root、split_path、PHP 路径拆分；`TestFirstSplit`（`:273`）校验 `index.PHP/somewhere` 切分 | L13 | 低 | 手写路径分段扫描；表驱动输入，不触碰真实文件系统 |
| `caddyconfig/caddyfile/parse_test.go:373` | 递归 import：文件 A import B、B import A，必须检测循环并报错；绝对/相对路径 | L04/L16 | 中 | 递归解析 + `map[path]bool` 访问集；深度上限防栈溢出 |
| `caddytest/integration/stream_test.go:22` | HTTP/2→h2c 流式请求：`io.Pipe` 写 body，flush 后读响应；defer 用 1ns timeout 调 `server.Shutdown`，验证流式连接不被优雅关闭卡死 | L19/L02 | 高 | `net/http` 服务 + `io.Pipe` + `http.Flusher`；`Shutdown` 用已取消 context，断言连接关闭；需处理 goroutine 退出 |
| `caddytest/integration/stream_test.go:206` | 大响应（1KB 随机串）经 gzip/chunked 回传，逐字节比对 | L19/L05 | 中 | `httptest.Server` + gzip writer + chunked reader；限时读完，GC 穿插 |
| `modules/caddyhttp/reverseproxy/upstreams_test.go:5` | IPv4/IPv6 三态指针（nil/true/false）归一化为 `ip/ip4/ip6` | L06/L13 | 低 | `*bool` 三态解析；表驱动，覆盖全组合 |

## 推荐收录（14 条）

1. `l02_concurrent_config_load` — `admin_test.go:147`。100 goroutine 并发 Load+provision 配置；P1，L02/L12/L06。核心是“读多写少的全局配置切换 + 反射解码 + `-race`”。
2. `l06_config_any_tree` — `admin_test.go:51`。JSON `any` 树的增删改与数组下标、`...` 递归展开；P1，L06/L13。可加 GC 与深层嵌套。
3. `l06_module_registry_prefix` — `modules_test.go:22`。全局模块表 + 前缀查询；P0，L06/L12/L02。补充并发 Register/GetModules 压测。
4. `l06_load_module_reflect` — `context_test.go:22`。struct tag 反射 + `json.RawMessage` + 接口断言（标量/数组/map 三形态）；P1，L06/L12。
5. `l02_replacer_concurrent` — `replacer_test.go:26`（叠加 `:240`）。占位符扫描 + provider 链 + RWMutex map；P1，L02/L13/L06。并发 ReplaceAll/Set/Delete，GC 打断。
6. `l06_responsewriter_readerfrom` — `modules/caddyhttp/responsewriter_test.go:47`。接口包装与 `io.ReaderFrom` 动态分派；P0，L06。适合验证 itab/断言路径。
7. `l19_streaming_shutdown` — `caddytest/integration/stream_test.go:22`。流式未结束时的 1ns Shutdown；P1，L19/L02。放回环 TCP，断言 goroutine 收敛。
8. `l05_template_pool_nested` — `modules/caddyhttp/templates/tplcontext_test.go:190`。嵌套 include + `sync.Pool` buffer；P1，L05/L04/L16。
9. `l13_lexer_long_tokens` — `caddyconfig/caddyfile/lexer_test.go:21`。heredoc/转义/行号状态机；P1，L13/L16/L05。长输入放大分配与 GC。
10. `l02_upstream_select_atomic` — `modules/caddyhttp/reverseproxy/selectionpolicies_test.go:36`。原子轮转 + 健康/满载跳过；P1，L02/L13。多 goroutine 并发 Select，校验计数与分布。
11. `l06_metrics_sync_once` — `modules/caddyhttp/metrics_test.go:30`。`sync.Once` 初始化 + in-flight gauge 归零；P1，L02/L06。
12. `l13_netaddr_parse` — `listeners_test.go:178`。网络地址/端口范围/unix 权限解析；P0，L13。
13. `l04_deep_placeholder_escape` — `replacer_test.go:26`（合成放大）。把嵌套花括号/转义深度放大到 10k–1M，触发 morestack 与栈复制；P1，L04/L13。原测试只覆盖浅层，需在 case 中显式生成深输入。
14. `l13_header_wildcards` — `modules/caddyhttp/headers/headers_test.go:29`。通配 header 删除与 Add/Set 语义；P1，L05/L13。

## 不可移植清单

- **真实 Caddy 服务/端口**：`caddytest/integration/**`（`caddyfile_test.go`、`reverseproxy_test.go`、`listener_test.go`、`stream_test.go`、`sni_test.go`）。依赖 `caddytest.Tester` 启动真实 admin/HTTP 端口、Caddyfile 适配和 TLS；只能把流式/关闭语义降级为 `net.Pipe`/`httptest` 模型。
- **TLS/ACME/证书/PKI**：`caddytest/integration/acme_test.go`、`acmeserver_test.go`、`autohttps_test.go`、`leafcertloaders_test.go`、`mockdns_test.go`、`modules/caddytls/**`（`capools_test.go`、`connpolicy_test.go`、`matchers_test.go`、`leaffileloader_test.go`）。依赖 certmagic、ACME responder、证书文件、libdns provider、x509 链验证与外部 DNS。
- **DSL/第三方引擎**：`modules/caddyhttp/celmatcher_test.go`（cel-go CEL 表达式）、`modules/caddyhttp/templates/tplcontext_test.go` 的 markdown/frontmatter/file listing 用例（goldmark 等文件依赖）。表达式语义与模板函数库无法用 stdlib 等价复刻。
- **文件系统/日志落盘**：`modules/logging/filewriter_test.go`、`modules/caddyhttp/fileserver/*_test.go`（真实 testdata 目录）、`caddyconfig/caddyfile/parse_test.go` 的 import 用例。依赖 umask、文件权限、日志滚动/压缩、目录遍历与 testdata。
- **FastCGI/反向代理协议**：`modules/caddyhttp/reverseproxy/fastcgi/client_test.go`（`DisabledTest` 起 FastCGI 服务）、`httptransport_test.go` 的 TLS 配置断言。依赖 FastCGI 帧、真实网络和证书配置；只能复用“缓冲/流式/超时”机制。
- **管理 API/证书链**：`admin_test.go` 中 `TestReplaceRemoteAdminServer`、`TestETags` 的证书/PKI 部分、`TestAdminRouterProvisioning` 的模块 provisioning。依赖 certmagic 实例、PEM 证书、prometheus 注册表与真实 admin 路由。
- **CLI/环境**：`cmd/main_test.go`、`caddytest/caddytest_test.go`。依赖命令行参数、环境变量、Caddyfile 路径与二进制行为。
- **第三方日志/指标**：`modules/caddyhttp/server_test.go` 的 zap encoder 断言、`modules/metrics/metrics_test.go`（prometheus client）。场景可借鉴，但原代码不能脱离 zap/prometheus 编译。
- **构建/平台**：`modules/logging/filewriter_test.go` 顶部 `//go:build !windows`，`listeners_test.go` 的 unix socket/权限位用例。跨平台 profile 下不成立，需按场景重写而非复制。
- **配置适配器全链路**：`caddyconfig/httpcaddyfile/*_test.go`、`caddytest/integration/caddyfile_adapt_test.go`。依赖完整模块注册表与 JSON 适配产物；只适合提炼“token→路由配置”的纯数据变换。
