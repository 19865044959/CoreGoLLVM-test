# frp v0.67.0

- 来源：`research/repos/frp`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / `README`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/frp`
- 本地核验：`git describe` = `v0.67.0`，HEAD `5f575b84`（2026-01-31，`Merge pull request #5147 from fatedier/dev`）
- 模块：`github.com/fatedier/frp`，`go 1.24.0`
- 范围：该提交上游仅 14 个 `*_test.go`（全部已读）：`pkg/config`、`pkg/config/v1`、`pkg/config/types`、`pkg/util/{metric,net,util,vhost}`、`pkg/proto/udp`、`pkg/auth`、`test/e2e`；文件:行均相对本仓根目录。
- 说明：frp 的并发/代理核心测试（server/proxy、client/proxy、transport、nathole）未随 partial clone 提供，`test/e2e` 依赖未被 clone 的 ginkgo framework 与 frpc/frps 二进制。因此 L02/L04 只能从现有测试提炼“goroutine + accept + TLS 嗅探、原子/互斥计数、配置反射解码”，并用合成压力补足；不能声称复刻 frp 隧道数据面。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| `pkg/util/metric/counter_test.go:9` | 计数器 Inc/Dec/Count/Snapshot/Clear，快照与清零后的可见性 | L02/L13 | 低 | `atomic.Int64` + `Snapshot()` 读取；并发 100 goroutine Inc/Dec 后校验总和 |
| `pkg/util/metric/date_counter_test.go:9` | 按日桶计数：今日 Inc/Dec、`GetLastDaysCount(3)` 返回 3 天窗口、Snapshot | L02/L13 | 中 | `sync.Mutex` + `[N]int64` 环形日桶；可注入日期偏移，校验跨日滚动与并发写 |
| `pkg/util/vhost/https_test.go:12` | 回环 TCP 监听 + accept goroutine；另一 goroutine 延迟 100ms 发起 TLS 握手，服务端从 ClientHello 解析 ServerName，得到 `Host=example.com`/`Scheme=https` | L02/L19/L04 | 中 | `net.Listen` + `tls.Client`/手工解析 ClientHello；用 accept/dial 两个 goroutine + deadline 替代 sleep 同步；关闭 listener 后检查 accept 循环退出 |
| `pkg/proto/udp/udp_test.go:9` | UDP 包构造与 `GetContent` 往返，内容一致 | L05/L13 | 低 | 自定义 header+payload 帧（长度前缀）；`[]byte` 拼接与切分，GC 后比对 |
| `pkg/config/v1/proxy_test.go:24` | 配置里 `"type":"tcp"/"http"` 经 `json.Unmarshal` 分派为 `TCPProxyConfig`/`HTTPProxyConfig`，再断言 `ProxyConfigurer` 具体类型 | L06 | 低 | 自定义 `UnmarshalJSON` + type 字段 switch；接口切片 `[]ProxyConfigurer` 断言具体类型 |
| `pkg/config/v1/value_source_test.go:24` | `ValueSource` 的 nil/未知 type/缺 File 的合法性校验；`Validate()` 统一入口 | L06 | 低 | 接口 + 表驱动校验；`nil` 接收者/未知枚举分支 |
| `pkg/config/v1/value_source_test.go:126` | `FileSource.Resolve`：读取文件、trim 首尾空白、拒绝路径穿越 `../../../etc/passwd`、不存在文件报错 | L06/L19 | 中 | `os.ReadFile` + `filepath.Clean`/`strings.HasPrefix` 防穿越；可用 `fstest.MapFS` 替代真实文件 |
| `pkg/config/v1/value_source_test.go:184` | `ValueSource.Resolve` 按 type 分派到 File，未知 type/穿越路径报错 | L06 | 低 | 接口/枚举分派 + context 传递（`Resolve(ctx)`） |
| `pkg/auth/oidc_test.go:24` | mock `TokenVerifier` 接口；同一 privilege key 的 Login/Ping subject 必须一致：空 subject 报错（`:24`）、新 subject 成功（`:35`）、不同 subject 报错（`:50`） | L06/L02 | 中 | 定义 `Verifier interface{ Verify(ctx, subject) (Token,error) }`；`map[privilegeKey]subject` + RWMutex；并发 Ping 校验绑定关系 |
| `pkg/config/load_test.go:59` | 同一 ServerConfig 从 TOML/YAML/JSON 三种文本加载，字段值与嵌套 `transport.tcpKeepalive` 一致 | L06/L13 | 中 | 仅保留 JSON（stdlib）；自定义 YAML/TOML 最小解析不现实，重写为“同一结构体多编码往返” |
| `pkg/config/load_test.go:85` | strict 模式拒绝未知字段（`bindAdur` 报错），非 strict 忽略并保留零值 | L06 | 低 | `json.Decoder.DisallowUnknownFields()` 对比宽松解码；错误文本包含字段名 |
| `pkg/config/load_test.go:115` | 配置模板渲染：`{{ 123 }}`、`{{ "xyz" }}`、`{{ printf "%q" ... }}` 输出数字/字符串/引号 | L16/L05 | 低 | `text/template` + `FuncMap`；表驱动渲染，GC 后比对输出 |
| `pkg/config/types/types_test.go:29` | `BandwidthQuantity` 的 JSON 编解码：`"1KB"` ↔ 字节数，与普通 int 字段共存 | L13/L06 | 低 | 自定义 `MarshalJSON/UnmarshalJSON`；字符串单位解析 + 结构体往返 |
| `pkg/config/types/types_test.go:42` | `PortsRangeSlice.String()` 把 `{Start,End}`/`{Single}` 输出为 `1000-2000,3000`；`:58` 反向解析 | L13/L05 | 低 | 手写 range 解析/格式化；边界（单端口、跨段、非法）表驱动 |
| `pkg/util/util/util_test.go:23` | `ParseRangeNumbers("3-5,8")` 展开为 `[3,4,5,8]`，容忍空格，非法输入报错 | L05/L13 | 低 | `strings.Split` + `strconv`；展开时预分配切片，末尾校验 |
| `pkg/util/net/proxyprotocol_test.go:11` | 构造 PROXY protocol v1/v2 头：TCP/UDP、IPv4/IPv6、nil 地址、Unix 地址；`:85` 校验 AddressFamily/Protocol/Command 结构字段 | L13/L05 | 中 | 手写 v1 文本/v2 二进制编码（`encoding/binary` + 地址族判断）；解析回来逐字段比对 |
| `pkg/util/util/util_test.go:9` | `RandID()` 固定返回 16 字符；`GetAuthKey` 对固定输入的 MD5 常量结果 | L13 | 低 | crypto/rand + base64/hex；常量向量校验确定性 |
| `pkg/config/v1/client_test.go:24` | `ClientConfig.Complete()` 填充默认值：auth=token、TCPMux、LoginFailExit、TLS 默认开启；`server_test.go:24` 同理 | L06/L13 | 低 | 零值结构 + `Complete()` 逐字段填默认；指针三态默认（nil→true）断言 |
| `test/e2e/e2e_test.go:41` | Ginkgo E2E 入口，注册 legacy/v1 的 basic/features/plugin 套件，启动真实 frpc/frps | L19/L02 | 高 | 仅可借鉴“进程/端口/配置目录”编排；stdlib 下用 `os/exec` + 随机端口重写，不属于稳定性用例 |

## 推荐收录（15 条）

1. `l02_counter_atomic` — `pkg/util/metric/counter_test.go:9`。原子计数 + 快照/清零；P0，L02/L13。补并发 Inc/Dec 与 GC。
2. `l02_date_counter_rollover` — `pkg/util/metric/date_counter_test.go:9`。按日桶计数与跨日滚动；P1，L02/L13。注入日期，避免真实跨天。
3. `l19_tls_hostname_sniff` — `pkg/util/vhost/https_test.go:12`。accept 循环 + TLS ClientHello 解析；P1，L19/L02/L04。原测试靠 sleep 且全局 conn 有竞态，重写时用 channel/deadline 同步并加 `NumGoroutine` 收敛断言。
4. `l06_typed_proxy_config` — `pkg/config/v1/proxy_test.go:24`。`type` 字段 → 具体实现 + 接口断言；P0，L06。
5. `l06_value_source_resolve` — `pkg/config/v1/value_source_test.go:24`（叠加 `:126`/`:184`）。接口校验/分派 + 文件读取 + 路径穿越防护；P1，L06/L19。用 `fstest.MapFS` 去除真实文件依赖。
6. `l13_ports_range_parse` — `pkg/config/types/types_test.go:42`。端口范围格式化/解析；P0，L13/L05。
7. `l13_bandwidth_json` — `pkg/config/types/types_test.go:29`。自定义 JSON 单位编解码；P0，L13/L06。
8. `l13_proxy_protocol_header` — `pkg/util/net/proxyprotocol_test.go:11`。v1/v2、IPv4/IPv6/UDP 二进制头构造；P1，L13/L05。按 RFC 手写编码，不引第三方。
9. `l05_udp_packet_roundtrip` — `pkg/proto/udp/udp_test.go:9`。长度前缀帧往返；P0，L05/L13。
10. `l06_oidc_subject_binding` — `pkg/auth/oidc_test.go:24`。Verifier 接口 + privilege key→subject 状态；P1，L06/L02。补 100 并发登录/心跳，验证无串号。
11. `l16_config_template_render` — `pkg/config/load_test.go:115`。`text/template` 数字/字符串/printf；P0，L16/L05。
12. `l06_config_defaults_tristate` — `pkg/config/v1/client_test.go:24`（叠加 `server_test.go:24`）。零值 + 指针默认值填充；P0，L06/L13。
13. `l02_config_load_concurrent` — `pkg/config/load_test.go:59`（合成放大）。同一 JSON 配置被 N goroutine 反复解码到结构体并校验；P1，L02/L06/L13。原测试串行，需显式加并发。
14. `l13_parse_range_numbers` — `pkg/util/util/util_test.go:23`。范围字符串展开与预分配；P0，L13/L05。
15. `l04_deep_config_reflect` — `pkg/config/load_test.go:59`（合成放大）。生成 10k 层嵌套 JSON，解码为 `map[string]any` 后递归反射遍历；P1，L04/L06。以深度触发 morestack/栈复制，失败判读限定为栈溢出或遍历遗漏。

## 不可移植清单

- **E2E/真实隧道**：`test/e2e/e2e_test.go`。依赖 ginkgo、frpc/frps 二进制、随机端口、真实 TCP/UDP/QUIC、插件进程与配置文件；partial clone 连 `test/e2e/framework` 都没有，无法编译。
- **第三方配置格式**：`pkg/config/load_test.go` 的 TOML/YAML 分支。依赖 `pelletier/go-toml`、`yaml.v3` 的 merge/严格模式语义；stdlib 只有 JSON，不能等价覆盖。
- **OIDC**：`pkg/auth/oidc_test.go` 使用 `go-oidc/v3` 的 `oidc.IDToken` 类型。场景可移植（接口 mock），代码不能独立编译。
- **PROXY protocol**：`pkg/util/net/proxyprotocol_test.go` 依赖 `pires/go-proxyproto` 的常量与结构。头格式可手写，但不能复制原实现。
- **文件型 ValueSource**：`pkg/config/v1/value_source_test.go:126` 依赖真实临时文件、权限位与路径穿越判断；可用 `fstest.MapFS` 改写，但原测试的 `t.TempDir`/`os.WriteFile` 不适合只读/受限环境。
- **TLS 嗅探**：`pkg/util/vhost/https_test.go` 依赖真实 TLS ClientHello 与证书配置（`InsecureSkipVerify`）。回环可复现，但测试本身用 100/200ms sleep 同步且有数据竞争，不能原样搬运。
- **默认证书/日志/平台**：`pkg/config/v1/client_test.go`/`server_test.go` 的 `Complete()` 只是默认值断言，可移植；但 `pkg/util/vhost/https_test.go` 的 TLS 与 `test/e2e` 的插件（plugin）路径受平台/构建模式限制。
- **插件/第三方代理**：`test/e2e/legacy/plugin`、`test/e2e/v1/plugin`（未 clone）。依赖外部插件二进制与 go-plugin 协议。
