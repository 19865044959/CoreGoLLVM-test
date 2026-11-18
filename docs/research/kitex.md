# kitex v0.13.0

- 来源：`research/repos/kitex`（partial clone，仅 `*_test.go`、`go.mod`、README；commit `1dd7526b`，`git describe --tags` = `v0.13.0`）
- 模块：`github.com/cloudwego/kitex`，`go.mod` 声明 `go 1.19`
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/kitex`
- 范围：仓内 242 个 `*_test.go`。按 `generic|sync|atomic|go func|chan|Once|init|Register|template|codec` 过滤后，精读 `pkg/remote/trans/ttstream/container`、`pkg/utils`、`pkg/mem`、`pkg/remote/trans/nphttp2/grpc`、`pkg/remote/codec/thrift`、`pkg/loadbalance/lbcache`、`pkg/stats`、`pkg/klog`、`client`、`tool/internal_pkg/**`。文件:行均相对本仓根目录。
- 重点维度：L01 泛型、L12 初始化/注册表/链接、L02 并发、L10 代码生成；附带 L05/L13/L18/L19。
- 说明：
  - kitex 的 `pkg/generic` 是"泛化调用"（IDL/JSON/Map 动态映射），**不是 Go 类型参数**。v0.13.0 测试代码里的 Go 泛型证据只有 `pkg/remote/trans/ttstream/container` 的 `NewQueue[T]`、`NewStack[T]`、`NewPipe[T]` 共 8 处实例化，没有泛型函数声明或泛型别名。L01 用例应以这三个容器为骨架；泛化调用只作反射/数据映射（L06/L13）来源。
  - partial clone 未取 `internal/mocks`（仅有 README）和 `testdata/idl`，因此所有 import `internal/mocks`、`gomock`、`cloudwego/thriftgo`、`cloudwego/dynamicgo` 的测试在本仓不可直接编译；本文件只提炼其中的纯逻辑。
  - 不含 cgo；未使用 `go:linkname`/`//go:noescape`；生成代码通过 `protoc-gen-go`/Kitex 模板以源码形式进入测试包。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| `pkg/remote/trans/ttstream/container/queue_test.go:26` | `NewQueue[int]()`，单生产者 goroutine 写 10 万次 1，主 goroutine `Get` 累加直到 10 万，末尾 `Size()==0` | L01/L02 | 低 | 泛型 `Queue[T]` + 互斥链表/环形缓冲；生产者/消费者各一 goroutine，sum 与空队列断言 |
| `pkg/remote/trans/ttstream/container/stack_test.go:26` | `NewStack[int]()`，交替 `Pop`/`PopBottom` 消费 10 万次 | L01/L02 | 低 | 泛型双端栈：头尾各取，校验总和 |
| `pkg/remote/trans/ttstream/container/stack_test.go:88` | `RangeDelete(func(v int)(deleteNode,continueRange bool))` 删除偶数，`Size` 恰减半，再遍历一次确认剩余全奇数 | L01/L13 | 低 | 泛型链表 + 回调；回调返回"删除/继续"双布尔，GC 后复查链表 |
| `pkg/remote/trans/ttstream/container/pipe_test.go:30` | `NewPipe[int]()`，写 1 万轮×5 个元素，后台 goroutine `Read` 累加，`Close` 后读到 `io.EOF`，总量相等 | L01/L02 | 中 | 泛型 channel/ring 管道；写满阻塞、关闭后 EOF |
| `pkg/remote/trans/ttstream/container/pipe_test.go:69` | 先启动读 goroutine，再写 10 个元素，轮询读到 10 后 `Close`，读侧收 `ErrPipeEOF` 并退出 | L01/L02/L18 | 中 | 关闭/唤醒握手：close 标志 + 条件变量；GC 在关闭前 |
| `pkg/utils/max_counter_test.go:27` | 100 goroutine 并发 `Inc`，max=10 时恰 10 个成功；max=0/-1 时 0 个成功；`Dec` 100 次后内部值 -100 | L02 | 低 | `atomic.Int64` CAS 上限；成功计数用 atomic，确定性断言 |
| `pkg/utils/ring_test.go:134` | 容量 10 的 ring，100 goroutine `Push`（10 成功、90 返回错误），随后 100 goroutine `Pop`，弹出值必须在成功集合中 | L02/L13 | 中 | `sync.Mutex` + 固定数组环；成功位图 + 错误计数原子累加 |
| `pkg/mem/span_test.go:55` | 12 goroutine × 128 轮，对 3B/32B/63B/64B/1KB/32KB/64KB/128KB 做 `Copy`，逐轮比对内容与长度 | L02/L05 | 中 | 自建 size-class span/buffer；每轮穿插分配垃圾，末尾 `runtime.GC()` |
| `pkg/mem/span_test.go:87` | `SpanCache` 同样 12×128 并发复制，覆盖缓存命中/未命中路径 | L02/L05/L18 | 中 | `sync.Pool` 分桶 + 容量校验；GC 前后各跑一轮 |
| `pkg/remote/trans/ttstream/container/object_pool_test.go:41` | 每 key 对象池推入 1 万个对象，10µs 延迟后逐个 `Close` 回调并 `wg.Done`；`Close()` 后 Size=0；1s 延迟时先不回调 | L02/L18 | 中 | 定时器驱动的延迟关闭池；`Close` 排空并等待回调计数 |
| `pkg/remote/trans/nphttp2/grpc/controlbuf_test.go:27` | `put`/`get(block)` 单元素；`execute` 返回 false；塞满 `maxQueuedTransportResponseFrames+5` 后 `throttle()` 阻塞，由消费 goroutine 放行 | L02/L19 | 中 | 有界队列 + 条件变量；throttle 用水位门限，`finish` 关闭唤醒 |
| `pkg/remote/trans/nphttp2/grpc/flowcontrol_test.go:27` | `writeQuota.get(120)` 在配额不足时阻塞，另一 goroutine `replenish(40)` 后返回；`done` 通道关闭时返回 `errStreamDone` | L02/L19 | 低 | 信号量 + `select` 等待 quota/stop；边界 80/120/20 |
| `client/rpctimeout_pool_test.go:32` | 100 个任务并发 `RunTask`，全部执行且 ctx 被 cancel；2×maxIdleTime 后 worker 池 Size 归 0 | L02/L19 | 中 | worker 池 + idle timer；用可注入 clock 或缩短参数，避免长 sleep |
| `pkg/remote/connpool/long_pool_test.go:807` | 连接池淘汰到 minIdle：每个连接 `runtime.SetFinalizer` 计数，3×idleTime 后反复 `runtime.GC()`，存活连接数收敛到 minIdle，`globalIdle` 同步下降 | L18/L02 | 高 | finalizer + 池计数；GC 轮询替代 sleep，断言最终稳定值 |
| `pkg/remote/trans/netpollmux/shard_map_test.go:27` | 32 分片 map：key 0/负数不命中，1..1023 store/load/delete；`rangeMap` 遍历和恰为 1023 | L02/L13 | 低 | `[32]sync.Map` 或分片互斥 map；key 边界与遍历计数 |
| `pkg/event/bus_test.go:27` | EventBus `Watch`/`Unwatch`/`Dispatch`：异步回调只命中订阅者；`:83` `DispatchAndWait` 并发执行并等待全部完成 | L02/L12 | 低 | `sync.Map` 事件→回调切片注册表 + 每回调一个 goroutine；WaitGroup 收口 |
| `pkg/limiter/qps_limiter_test.go:33` | 3 goroutine 连续 `Acquire` 2s，QPS 误差 <2%；`UpdateQPSLimit` 后 <5%；`UpdateLimit(0)` 时失败计数为 0 | L02/L19 | 高 | 令牌桶 + 可控时钟；P2，默认缩为 1/100 时间尺度 |
| `pkg/remote/trans/nphttp2/mocks_test.go:706` | `protoc-gen-go` 生成文件的 `init()` 调用 `file_message_proto_init()`；包级 `rawDesc []byte`、`sync.Once` 懒 gzip、构建完把中间表置 nil | L10/L12 | 中 | 手写"生成物"：`var rawDesc=[]byte{...}` + `Once` + `init()`，二次调用幂等 |
| `pkg/remote/trans/nphttp2/status/mock_test.go:147` | 另一份生成 protobuf 的 `init()`/`rawDescOnce`/`rawDescGZIP` 同构场景 | L10/L12 | 中 | 同上；可与上一条合并为一个多文件生成物用例 |
| `pkg/stats/event_test.go:25` | 全局事件注册表：`DefineNewEvent` 新增后 `MaxEventNum` +1；重名返回 `ErrDuplicated` 且不增；`FinishInitialization` 后新增返回 `ErrNotAllowed` | L12 | 低 | 包级 map + 序号；冻结标志；GC 前后各注册一轮 |
| `pkg/remote/codec/thrift/codec_apache_test.go:56` | 全局注册 `RegisterCheckTStruct`/`RegisterThriftRead`/`RegisterThriftWrite` 函数指针，覆盖默认实现；defer 里全部置 nil（注释说明无法恢复原值） | L12/L10 | 中 | 包级函数指针 + 读写分派；覆盖后再置 nil，验证 nil 路径报错 |
| `pkg/klog/default_test.go:125` | 包级 logger 的 output/level 可全局修改；表驱动校验 6 个级别的过滤与格式，defer 恢复默认 | L12 | 低 | 全局 logger 单例 + `SetOutput`/`SetLevel`；测试内保存/恢复 |
| `pkg/loadbalance/lbcache/cache_test.go:40` | 全局 `balancerFactories sync.Map` 按 `cacheKey(resolver,balancer,opts)` 共享 factor；`Cacheable` 时同 key 复用；`Dump()` 的 JSON 字典序固定 | L12/L02 | 中 | 包级注册表 + 引用计数 + 单飞；Dump 用排序后的固定 JSON |
| `pkg/loadbalance/lbcache/hookable_test.go:26` | 索引式 hook 注册/注销：索引 0..4 单调递增、注销后不复用，注销不存在索引不 panic | L12 | 低 | slice 槽位 + 墓碑标记；注销越界静默 |
| `tool/internal_pkg/generator/type_test.go:23` | `SetTemplateExtension` 只接受 `templateNames` 中的模板名，未知名被忽略；`:38` `AddTemplateFunc` 写入全局 `funcs` 表 | L10/L12 | 低 | 包级 map 白名单 + 函数表；断言未注册键不存在 |
| `internal/configutil/once_test.go:25` | `OptionOnce.OnceOrPanic`：首次成功，第二次及以后 panic | L12 | 低 | `atomic.Bool`/`sync.Once` + panic；recover 计数 |
| `pkg/generic/thriftidl_provider_test.go:410` | `thrift.SetDefaultParseMode(LastServiceOnly)` 改全局默认；per-provider `WithParseMode(...)` 只影响本次解析，全局值不被改 | L12/L10 | 中 | 包级默认 + option 覆盖；解析结果选择首个/合并 service |
| `pkg/remote/trans/ttstream/ktx/ktx_test.go:24` | `RegisterCancelCallback` 注册到 ctx，`cancelFunc()` 后回调恰好执行 1 次 | L12/L19 | 低 | ctx 内回调切片 + once；重复 cancel 不重复回调 |
| `tool/internal_pkg/tpl/pbtpl/pbtpl_test.go:25` | 用模板 `Render` 生成 4 种流式服务代码（StreamX true/false），再交给 `go/format` 解析；语法错误即失败 | L10 | 中 | `text/template` 渲染 + `go/parser`/`go/format` 校验；固定输入结构体 |
| `tool/internal_pkg/generator/generator_test.go:26` | `Config.Pack()` 反射按 39 个字段顺序输出 `k=v` 列表；`:107` `Unpack` 仅接受 `Features=N` | L10/L06 | 中 | 反射遍历 struct 字段（含 `feature` 位图）+ 字符串编解码；逐字段 golden |
| `tool/internal_pkg/util/util_test.go:24` | `CombineOutputPath` 展开 `{namespace}`/`{namespaceUnderscore}` 两种路径占位符 | L10 | 低 | `strings.ReplaceAll` + 状态机；4 组路径表 |
| `pkg/protocol/bthrift/binary_test.go:336` | `Binary.Length` 对 message/struct/field/map/list/set/各标量分别计算编码长度 | L10/L11 | 低 | `encoding/binary` 手写 thrift 编码；逐类型断言长度 |
| `pkg/protocol/bthrift/binary_test.go:460` | `TestSkip`：混合 byte/string/double/bool/i16/i32/i64/list/map/set 编码总长 46，按类型跳过并读取后续字段 | L10/L13 | 中 | 手写 wire 编解码 + Skip；字节级 golden |
| `internal/utils/safemcache/safemcache_test.go:26` | `Malloc` 分配的 slice 尾部写 magic footer，`Free` 清零；footer 被篡改后 `Free` 不生效 | L07/L18 | 中 | `unsafe.SliceHeader`/自定义头部 + magic 校验；GC 后校验 |

## 推荐收录用例（15 条）

| # | 优先级 | 建议用例名 | 来源 | 维度 | 重写要点 |
|---|---|---|---|---|---|
| 1 | P1 | `l01_generic_queue_concurrent` | `pkg/remote/trans/ttstream/container/queue_test.go:26` | L01/L02 | 泛型 `Queue[T]`，生产者 10 万次 `Add(1)`、消费者 `Get`；GC 每 2 万次；断言 sum=100000、Size=0 |
| 2 | P1 | `l01_generic_stack_range_delete` | `pkg/remote/trans/ttstream/container/stack_test.go:88` | L01/L13 | 泛型 `Stack[T]` + `RangeDelete(func(T)(bool,bool))`；输入 1..1000，删偶数；GC 前后 Size=500、剩余和=250500 |
| 3 | P1 | `l01_generic_pipe_close` | `pkg/remote/trans/ttstream/container/pipe_test.go:69` | L01/L02/L18 | 泛型 `Pipe[T]`：先读后写 10 个元素，读者见 10 后 Close；GC 在 Close 前；断言总和 55 且读侧收 EOF |
| 4 | P0 | `l02_max_counter_race` | `pkg/utils/max_counter_test.go:27` | L02 | `atomic.Int64` 上限计数器；100 goroutine ×3 组 max=10/0/-1，成功数恰 10/0/0；GC 在组间 |
| 5 | P1 | `l02_ring_parallel_push_pop` | `pkg/utils/ring_test.go:134` | L02/L13 | 容量 10 环：100 并发 Push（10 成功/90 失败）再 100 并发 Pop；GC 后校验无重复、无丢失 |
| 6 | P1 | `l02_span_copy_parallel` | `pkg/mem/span_test.go:55` | L02/L05 | 自建 size-class span；12 goroutine ×128 轮覆盖 8 种尺寸；每 16 轮 `runtime.GC()`；逐轮内容/长度断言 |
| 7 | P1 | `l02_object_pool_delayed_close` | `pkg/remote/trans/ttstream/container/object_pool_test.go:41` | L02/L18 | 对象池 1 万对象 + 延迟关闭回调；GC 在 Push 后；断言 Close 后 Size=0、回调计数=10000 |
| 8 | P2 | `l02_timeout_pool_idle_shrink` | `client/rpctimeout_pool_test.go:32` | L02/L19 | 100 任务 worker 池；GC 在任务完成后；2×idle 时间后 Size=0；参数缩为 100ms 级 |
| 9 | P1 | `l18_connpool_evict_finalizer` | `pkg/remote/connpool/long_pool_test.go:807` | L18/L02 | 池淘汰到 minIdle：SetFinalizer 计数 + 3 轮 `runtime.GC()`；断言存活=minIdle、globalIdle 同步下降 |
| 10 | P0 | `l12_event_registry_freeze` | `pkg/stats/event_test.go:25` | L12 | 全局事件注册表：新增→重名 `ErrDuplicated`→`FinishInitialization` 后 `ErrNotAllowed`；GC 在冻结前，序号不倒退 |
| 11 | P1 | `l12_codec_global_func_registry` | `pkg/remote/codec/thrift/codec_apache_test.go:56` | L12/L10 | 包级函数指针注册/覆盖/置 nil；GC 在注册后；断言覆盖生效、nil 后走错误路径 |
| 12 | P1 | `l12_lbcache_factory_registry` | `pkg/loadbalance/lbcache/cache_test.go:40` | L12/L02 | 全局 factory 表 + Cacheable：同 key 两次构造返回同一实例；GC 后 Dump JSON 与 golden 一致 |
| 13 | P1 | `l12_generated_descriptor_init` | `pkg/remote/trans/nphttp2/mocks_test.go:706` | L10/L12 | 手写生成物形态：`rawDesc` 包级大字节切片 + `sync.Once` 懒压缩 + `init()`；GC 在首次使用后；二次调用指针不变 |
| 14 | P1 | `l10_thrift_binary_skip` | `pkg/protocol/bthrift/binary_test.go:460` | L10/L13 | 固定 46 字节 thrift 结构；GC 在编码后；按类型 Skip 全程并把读到的值依次断言 |
| 15 | P0 | `l10_codegen_template_render` | `tool/internal_pkg/tpl/pbtpl/pbtpl_test.go:25` | L10 | 固定 Service/Method 输入渲染 4 种流式组合，StreamX 开关 ×2；输出交 `go/format` 解析；GC 在渲染前，golden 比对生成文本 |

优先级说明：P0 为纯内存、无并发/无用力时序；P1 允许小规模并发、定时器或包级注册表；P2 需缩放时间参数。

## 不可移植清单

| 场景 | 代表位置 | 原因 |
|---|---|---|
| 真实服务端 + IDL 文件 | `pkg/generic/json_test/generic_test.go:65`、`:68`；`pkg/generic/map_test/generic_test.go`；`pkg/generic/binary_test/generic_test.go:170` | 启动 thrift/HTTP 服务并读取 `./idl/example.thrift`；依赖 dynamicgo/thriftgo 与本地端口，不能单包运行 |
| 本地监听 + 生成 stub | `pkg/generic/reflect_test/reflect_test.go:181`、`:183`（`net.ResolveTCPAddr` + `genericserver.NewServer`）；`server/server_test.go:809`（`net.Listen`） | 依赖真实 listener、客户端、服务注册表与端口分配；只能重写状态机 |
| netpoll FD 对 / 平台限制 | `pkg/remote/trans/ttstream/transport_buffer_test.go:30`、`pkg/remote/trans/ttstream/transport_test.go:58` | `netpoll.GetSysFdPairs`/`NewFDConnection`，文件带 `//go:build !windows`；stdlib 无等价 API |
| netpoll 传输 handler | `pkg/remote/trans/netpoll/http_client_handler_test.go:43`、`pkg/remote/trans/netpollmux/server_handler_test.go:65` | 依赖 netpoll `Connection` mock/真实 ByteBuffer 与包内未导出 handler；stdlib 只能做协议语义模拟 |
| 内部 mocks / gomock | `internal/mocks` 仅有 README；`client/option_test.go:652`、`pkg/remote/connpool/long_pool_test.go:48` | partial clone 未取 mock 实现，且 `internal` 包不可导入；这类测试在本仓连编译都过不了 |
| dynamicgo / thriftgo 动态解析 | `pkg/generic/thriftidl_provider_test.go:410`、`client/genericclient/generic_stream_service_test.go:31` | 依赖 `cloudwego/dynamicgo`、`thriftgo` 解析器与 `.proto/.thrift` 文件；可作为"全局默认 vs option"语义来源，不能直接搬 |
| frugal 生成代码 fast path | `pkg/remote/codec/thrift/codec_frugal_test.go:79` | 依赖 `cloudwego/frugal` 生成的 `FastRead/FastWrite` 与 `gopkg` codec；stdlib 无法复刻字节级 fast path |
| 代码生成器外部进程 | `tool/internal_pkg/prutal/prutal_test.go:28`、`tool/cmd/kitex/versions/dependencies_test.go:247` | 写临时 `.proto` 后调用 prutal/protoc，或 `exec` 调 `go` 查依赖；需要外部工具链 |
| pprof 采样与时序 | `pkg/profiler/profiler_test.go:32` | 依赖 100ms 采样窗口、`runtime/pprof` 标签与调度时机；结果非确定 |
| QPS 实测 | `pkg/limiter/qps_limiter_test.go:33` | 2s 墙钟吞吐误差断言，且文件带 `//go:build !race`；只能保留令牌桶状态机，P2 缩放 |
| 未导出内部类型 | `pkg/remote/trans/nphttp2/grpc/controlbuf_test.go:27`（`newControlBuffer`/`throttle`）、`pkg/remote/trans/nphttp2/grpc/flowcontrol_test.go:27`（`newWriteQuota`）、`pkg/mem/span_test.go:55`（`spanClass`/`NewSpan`） | 直接测包内未导出实现；必须按语义重写，不能断言内部字段布局 |
| benchmark 型场景 | `pkg/utils/ring_test.go:168`、`pkg/remote/trans/netpollmux/shard_map_test.go:76`、`pkg/remote/connpool/long_pool_test.go:917` | 只比较吞吐/分配次数，无确定性期望值；可借结构，不可作为稳定性用例 |

- cgo：未使用。
- runtime 内部结构：未直接引用；但 finalizer/GC 场景（`pkg/remote/connpool/long_pool_test.go:807`、`client/client_test.go:977`）依赖 2 万对象与 sleep，必须缩量并改成 GC 轮询。
- 结论：kitex 可稳定移植的是"泛型容器 + 原子/互斥并发原语 + 全局注册表/包级初始化 + thrift 二进制编解码 + 模板生成与 go/format 校验"；所有 netpoll、dynamicgo、frugal、内部 mocks、真实 listener 场景只保留状态机与语义。
