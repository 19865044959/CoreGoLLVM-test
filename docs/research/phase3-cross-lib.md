# Phase 3 跨库候选：L01 泛型 / L11 编译期优化 / L12 初始化与链接

- 来源：`docs/research/pgx.md`、`go-redis.md`、`grpc-go.md`、`gorm.md`、`kitex.md`，以及对应 `research/repos/<lib>` 的测试文件复核。
- 版本：pgx v5.7.4、go-redis v9.8.0、grpc-go v1.71.1、gorm v1.30.0、kitex v0.13.0。
- 口径：只收与 **L01 类型参数/实例化/泛型约束**、**L11 内联/逃逸/常量传播/DCE/边界检查消除**、**L12 init 顺序/包级变量/注册表/链接** 相关的场景；文件:行均已在本仓 partial clone 中确认存在。
- 可移植性标记：
  - **A**：纯内存、确定性输出，可直接改写为 stdlib-only `main.go`；
  - **B**：需自建替身（内存 rows / fake dialer / 可注入时钟 / 本地注册表），但语义可完整复现；
  - **C**：只能借场景与状态机，原测试依赖真实 DB、网络、外部工具或只做 benchmark，不能直接搬。

结论先行：

1. **L01 只在 pgx、gorm、kitex 有真实证据**。go-redis 和 grpc-go 的测试文件里没有类型参数声明或泛型实例化；`kitex/pkg/generic` 是"泛化调用"而非 Go 泛型。
2. **L11 没有现成的断言式测试**：五个库都只有 benchmark/热循环。用例必须改写成"语义 + 分配形态"断言（len/cap、指针是否复用、输出值），不能保留吞吐断言。
3. **L12 最富矿在 grpc-go 与 kitex**：全局 compressor/codec/事件/负载均衡注册表、生成代码 `init()`、包级 logger/模板表。风险是全局状态不可并行、不可重入，用例要么用本地注册表，要么显式保存/恢复。
4. 30 条建议中：L01 11 条、L11 9 条、L12 10 条；覆盖五个来源库。

## 一、候选场景池

### pgx v5.7.4

| 来源（库:文件:行） | 场景摘要 | 维度 | 可移植性 |
|---|---|---|---|
| `pgx:pgtype/range_codec_test.go:19` | `pgtype.Range[pgtype.Int4]` 空区间/闭开区间/零值/nil 的编解码往返 | L01 | B（真实 DB 往返改内存文本解析） |
| `pgx:pgtype/multirange_test.go:20` | `pgtype.Multirange[pgtype.Range[pgtype.Int4]]` nil/空/两段区间，嵌套泛型实例化 | L01 | B |
| `pgx:rows_test.go:335` | `pgx.CollectRows(rows, pgx.RowTo[int32])` 收集标量列 | L01 | B（用内存 Rows 替身） |
| `pgx:rows_test.go:371` | `pgx.RowToAddrOf[int32]` 返回 `*int32`，地址互不相同 | L01 | B |
| `pgx:rows_test.go:432` | `pgx.RowToStructByPos[person]` 按列位置填结构体，列数不匹配报错 | L01 | B |
| `pgx:rows_test.go:647` | `pgx.RowToStructByName[person]` 按列名填结构体，缺列报错 | L01 | B |
| `pgx:copy_from_test.go:833` | `pgx.RowTo[[]int64]`，元素类型本身是切片 | L01 | B |
| `pgx:pgtype/pgtype_test.go:307` | `Map.RegisterType(composite)` 后 `PlanScan` 解析 `"(1)"` | L12 | A（`NewMap` 与本地注册表纯内存） |
| `pgx:pgtype/pgtype_test.go:527` | 编码计划缓存 + UUID 数组类型混淆后回退文本格式（见 `pgx.md`） | L12 | A |
| `pgx:conn_test.go:1090` | `RegisterDefaultPgType(pgtype.Range[float64]{}, "examplefloatrange")` 按 Go 类型注册默认名 | L01/L12 | C（需连接与类型 OID） |
| `pgx:internal/pgio/write_test.go:23` | cap 足够的 buffer 上 `AppendUint16` 不新分配 | L11/L05 | A |
| `pgx:internal/iobufpool/iobufpool_test.go:10` | 0/1/255/256/257/32MB/32MB+1 的 size-class 分桶与 Get cap | L11/L05 | A |

### go-redis v9.8.0

| 来源（库:文件:行） | 场景摘要 | 维度 | 可移植性 |
|---|---|---|---|
| `go-redis:internal/pool/pool_test.go:16` | 连接池 Get/Put 后 Hits/Misses/Idle/Total 统计 | L12 | B（缺连时用 fake dialer） |
| `go-redis:internal/pool/pool_test.go:34` | MinIdleConns 建连未完成时 Close，计数归零、等待者退出 | L12 | B |
| `go-redis:internal/pool/pool_test.go:122` | MinIdleConns 初始化补连，Get/Remove 后维持下限 | L12 | B |
| `go-redis:internal/proto/writer_test.go:93` | `WriteBuffer` 反复 Append 4 个参数的分配基准 | L11 | C（benchmark，无期望值） |
| `go-redis:internal/proto/reader_test.go:71` | 8KB 单行只在末尾遇 CRLF，循环 `ReadSlice` 拼接 | L11/L13 | A |
| `go-redis:bench_decode_test.go:21` | 包级 `var initHello = []byte(...)` 在测试二进制启动时初始化 | L12 | A（可放大为包级大字节表） |
| `go-redis:example_test.go:18` | `init()` 构造全局 `rdb` client；包级 `ctx`/`rdb` 先于测试初始化 | L12 | B（fake client） |
| `go-redis:main_test.go:56` | 包级 `var cluster = &clusterScenario{ports..., clients: make(map...)}` 启动即分配 | L12 | C（依赖 Redis 拓扑） |
| `go-redis:internal/pool/bench_test.go:20` | 池大小 1/2/8/32/64/128 的并行 Get/Put 基准 | L11 | C（benchmark） |

### grpc-go v1.71.1

| 来源（库:文件:行） | 场景摘要 | 维度 | 可移植性 |
|---|---|---|---|
| `grpc-go:encoding/encoding_test.go:70` | 同名 compressor 重复注册覆盖旧实现、名字列表去重 | L12 | A（改本地 registry） |
| `grpc-go:internal/grpcsync/event_test.go:65` | `Event.Fire` 多 goroutine 并发只成功一次，Done 只关一次 | L12 | A |
| `grpc-go:internal/leakcheck/leakcheck_test.go:62` | `RegisterIgnoreGoroutine` 全局白名单注册，泄漏统计忽略之 | L12/L04 | B（需 `runtime.Stack`；可自建白名单） |
| `grpc-go:benchmark/primitives/primitives_test.go:365` | 热循环接口类型断言，itab 命中路径 | L11/L06 | C |
| `grpc-go:benchmark/primitives/primitives_test.go:449` | 接口方法调用 vs `:458` 具体类型方法调用（去虚化/内联） | L11 | C |
| `grpc-go:benchmark/primitives/primitives_test.go:170` | 闭包 `defer Unlock` vs `:187` 显式 Unlock | L11 | C |
| `grpc-go:internal/profiling/buffer/buffer_test.go:106` | 固定容量环形缓冲，双 goroutine 并发 Push/Drain 不丢不重 | L11/L02 | B |
| `grpc-go:resolver/map_test.go:74` | AddressMap 以 Address 为键，属性用 `Equal` 接口比较 | L12/L06 | A |
| `grpc-go:internal/cache/timeoutCache_test.go:51` | TTL cache Add 到期回调、Remove/Clear 不回调 | L12/L19 | B（fake timer） |
| `grpc-go:mem/buffer_pool_test.go:51` | 池化 buffer 复用前清零，GC 可插在 Get/Put 之间 | L11/L05 | A |

### gorm v1.30.0

| 来源（库:文件:行） | 场景摘要 | 维度 | 可移植性 |
|---|---|---|---|
| `gorm:tests/generics_test.go:21` | `gorm.G[User](DB)` 泛型链 Create/First/Take/Scan | L01 | C（每条都执行 SQL；改内存 Repo[T]） |
| `gorm:tests/generics_test.go:67` | `gorm.G[map[string]interface{}](DB)`，类型参数为 map 类型 | L01 | C |
| `gorm:tests/generics_test.go:772` | 5 goroutine 复用同一条泛型查询链，各自 Where 不得互相污染 | L01/L02/L11 | C（需 DB；语义可完全内存化） |
| `gorm:schema/schema_test.go:13` | `schema.Parse(&User{}, &sync.Map{}, NamingStrategy{})`，反射解析字段/关系并写缓存 | L12/L06 | B（自建模型与缓存） |
| `gorm:callbacks/create_test.go:14` | 包级 `var schemaCache = &sync.Map{}`，测试间共享 schema 缓存 | L12 | A |
| `gorm:tests/prepared_stmt_test.go:153` | 单连接 + 100 goroutine + PrepareStmt 缓存去重、不死锁 | L12/L02 | C（需 database/sql；可改成信号量+缓存） |
| `gorm:statement_test.go:11` | 1–8 层 `Statement.clone()` 后追加条件，原/副本互不污染 | L11/L13 | A |
| `gorm:clause/expression_test.go:16` | `clause.Expr` 的 `?` 占位符扫描与 Table/Column 片段拼接 | L11/L06 | A |

### kitex v0.13.0

| 来源（库:文件:行） | 场景摘要 | 维度 | 可移植性 |
|---|---|---|---|
| `kitex:pkg/remote/trans/ttstream/container/queue_test.go:26` | `NewQueue[int]()` 单生产者 10 万次 Add + 消费者 Get | L01/L02 | A |
| `kitex:pkg/remote/trans/ttstream/container/stack_test.go:88` | `NewStack[int]()` + `RangeDelete(func(int)(bool,bool))` 删偶数 | L01/L13 | A |
| `kitex:pkg/remote/trans/ttstream/container/pipe_test.go:69` | `NewPipe[int]()` 先读后写、Close 后 `ErrPipeEOF` | L01/L02/L18 | A |
| `kitex:pkg/protocol/bthrift/binary_test.go:460` | 混合 thrift 类型编码总长 46，按类型 Skip 后逐字段读取 | L10/L11/L13 | A |
| `kitex:pkg/utils/int_len_test.go:21` | `GetUIntLen` 对 0/5/10/56/102/99999 的位数表 | L11 | A |
| `kitex:pkg/stats/event_test.go:25` | 全局事件注册表：重名 `ErrDuplicated`、冻结后 `ErrNotAllowed` | L12 | A |
| `kitex:pkg/remote/codec/thrift/codec_apache_test.go:56` | 全局 `RegisterThriftRead/Write/CheckTStruct` 函数指针覆盖与置 nil | L12/L10 | A |
| `kitex:pkg/loadbalance/lbcache/cache_test.go:40` | 全局 `balancerFactories` 共享 factor、`Dump()` JSON 固定 | L12/L02 | B（singleflight + 排序 Dump） |
| `kitex:pkg/remote/trans/nphttp2/mocks_test.go:706` | protoc-gen-go 生成文件 `init()` + 包级 rawDesc + `sync.Once` 懒 gzip | L10/L12 | A（手写同构生成物） |
| `kitex:tool/internal_pkg/generator/type_test.go:23` | 模板扩展表白名单 + `AddTemplateFunc` 全局函数表 | L10/L12 | A |
| `kitex:tool/internal_pkg/tpl/pbtpl/pbtpl_test.go:25` | 模板渲染 4 种流式服务代码，交 `go/format` 解析校验 | L10 | A |
| `kitex:tool/internal_pkg/generator/generator_test.go:26` | `Config.Pack()` 反射按 39 字段顺序输出 `k=v` | L10/L06 | A |

## 二、30 条可重写用例建议

说明：所有用例均为单包 `package main`、只用 stdlib；最后一行 `ALL PASS <用例名>`，失败 `FAIL: ...` + 退出码 1。表中"输入"均为确定性数据，随机路径须用 `UAF_SEED`。

| # | 用例名 | 来源 | 维度 | 输入 | GC 位置 | 期望值 |
|---|---|---|---|---|---|---|
| 1 | `l01_rowto_scalar` | pgx:rows_test.go:335 | L01 | 内存 Rows 3 行，每行 1 个 int32：1/2/3 | 解码前 1 次；收集完 1 次 | `[]int32{1,2,3}`，len=3，sum=6 |
| 2 | `l01_rowto_addr` | pgx:rows_test.go:371 | L01 | 3 行 int32：7/8/9；用 `RowToAddrOf[int32]` | 第 2 行解码后、第 3 行前 | 3 个非 nil 且地址互异；`*p0=7`，改 `*p0=70` 后 `*p1` 仍 8 |
| 3 | `l01_rowtostruct_by_pos` | pgx:rows_test.go:432 | L01 | 2 行 `(id int32,name string)`：`(1,"a")`、`(2,"b")` | 收集完 1 次 | `[]person{{1,"a"},{2,"b"}}`；列数少 1 时返回错误且不 panic |
| 4 | `l01_rowtostruct_by_name` | pgx:rows_test.go:647 | L01 | 列顺序 `name,id`，2 行 `("a",1)`、`("b",2)` | 解码前 1 次 | 按名映射为 `{1,"a"}`、`{2,"b"}`；删除 id 列后返回"缺列"错误 |
| 5 | `l01_generic_range` | pgx:pgtype/range_codec_test.go:19 | L01 | 文本 `"[1,5)"`、`"(,3]"`、`"empty"` | 每个区间解析后各 1 次 | `Lower=1,Upper=5` 闭开；`Upper=3,UpperType=Unbounded`；empty 的 `Valid=true` 且上下界为空 |
| 6 | `l01_generic_multirange` | pgx:pgtype/multirange_test.go:20 | L01 | `"{[1,5),[7,9)}"` | 解析完 1 次 | 2 个元素，边界依次 `[1,5)`、`[7,9)`；空串得到 len=0 |
| 7 | `l01_generic_repo_chain` | gorm:tests/generics_test.go:21 | L01 | 内存 `Repo[User]` 预置 `{ID:1,Name:"a"}`；`Where(Name=="a").First()` | First 前 1 次 | `ID=1,Name="a"`；`Where(Name=="x")` 得到 `ErrNotFound` |
| 8 | `l01_generic_chain_reuse` | gorm:tests/generics_test.go:772 | L01 | 5 goroutine，每个 `base.Where("i=?",i)`，base 为 `Repo[int]` | 两轮并发之间 1 次 | 每个 goroutine 只得到自己的 i，5 个结果排序后 `0..4`，无串值 |
| 9 | `l01_generic_queue` | kitex:pkg/remote/trans/ttstream/container/queue_test.go:26 | L01 | 生产者 100000 次 `Add(1)`；消费者 `Get` 累加 | 每 10000 次 1 次 | sum=100000，最终 `Size()==0`，无丢唤醒 |
| 10 | `l01_generic_stack_range` | kitex:pkg/remote/trans/ttstream/container/stack_test.go:88 | L01 | 1..1000 入栈；`RangeDelete(v%2==0)` | 删除前 1 次、删除后 1 次 | `Size=500`；剩余元素全为奇数，和=250000 |
| 11 | `l01_generic_pipe_close` | kitex:pkg/remote/trans/ttstream/container/pipe_test.go:69 | L01 | 写 1..10，随后 Close；读者先启动 | Close 前 1 次 | 读到 10 个、sum=55，随后返回 `EOF`；重复 Close 不 panic |
| 12 | `l11_append_noalloc` | pgx:internal/pgio/write_test.go:23 | L11 | `buf := make([]byte,0,4)`；追加 uint16 0x1234 | 追加后 1 次 | len=2、cap=4（未扩容）、字节 `{0x12,0x34}`；再追加 2 字节仍 cap=4 |
| 13 | `l11_size_class` | pgx:internal/iobufpool/iobufpool_test.go:10 | L11 | 尺寸 0/1/255/256/257/32MB/32MB+1 | 每档分配后 1 次 | 每档 `cap>=size`；≤32MB 时 cap 落在 2 的幂桶，>32MB 精确分配；Put/Get 后内容标记仍在 |
| 14 | `l11_readline_8k` | go-redis:internal/proto/reader_test.go:71 | L11 | 8192 个 'a' + `\r\n`，底层 reader 按 4096/1/4096 分片 | 解析前 1 次 | 返回行 len=8192、首尾均为 'a'；第二次读返回 false/io.EOF，无残留 |
| 15 | `l11_nop_devirt` | grpc-go:benchmark/primitives/primitives_test.go:449,458 | L11 | 同一 Nop 实现分别经接口变量/具体类型调用 1e6 次 | 第 50 万次后 1 次 | 两个计数均=1000000，结果一致；无 panic |
| 16 | `l11_defer_unlock` | grpc-go:benchmark/primitives/primitives_test.go:170,187 | L11 | 10 万次加锁；A 用闭包 defer Unlock，B 显式 Unlock | 第 5 万次后 1 次 | 两路计数均=100000，最终锁可再次获取（无死锁） |
| 17 | `l11_where_clone` | gorm:statement_test.go:11 | L11 | 初始 1 个条件，连续 8 次 clone 后各追加 `FINALn` | 每次 clone 后 1 次 | 原语句条件数=1；第 n 层副本=n+1；各副本 FINAL 标记不互相出现 |
| 18 | `l11_expr_placeholders` | gorm:clause/expression_test.go:16 | L11 | `"id = ? AND name = ?"`，参数 `{1,"a"}`；标识符 Table/Column | 构建后 1 次 | 占位符恰 2 个；vars 顺序 `[1,"a"]`；输出含带引号的表列名 |
| 19 | `l11_int_len` | kitex:pkg/utils/int_len_test.go:21 | L11 | 值 0/9/10/99/100/99999/uint64max，循环 100 万次轮转 | 第 50 万次后 1 次 | 位数 1/1/2/2/3/5/20；所有轮次结果不变 |
| 20 | `l11_binary_skip` | kitex:pkg/protocol/bthrift/binary_test.go:460 | L11/L13 | byte/string/double/bool/i16/i32/i64/list/map/set 混合编码 | 编码后 1 次 | 总长 46；Skip 后 offset=46；依次读到 `'1',"2",3,'4',true,6,7,8,9,10,11` |
| 21 | `l12_event_registry_freeze` | kitex:pkg/stats/event_test.go:25 | L12 | `DefineNewEvent("myevent")` 两次；`FinishInitialization` 后再定义 `"another"` | 两次定义之间 1 次 | 第一次 err=nil 且序号 +1；第二次 `ErrDuplicated` 且序号不变；冻结后 `ErrNotAllowed` 且序号不变 |
| 22 | `l12_codec_global_funcs` | kitex:pkg/remote/codec/thrift/codec_apache_test.go:56 | L12 | 包级注册表设 read/write/check 三个函数，再全部置 nil | 注册后 1 次 | 非 nil 时分派到注册实现；置 nil 后调用返回确定错误（不 panic） |
| 23 | `l12_lbcache_factory` | kitex:pkg/loadbalance/lbcache/cache_test.go:40 | L12 | 同 resolver/balancer/options 构造两次，`Cacheable=true` | 两次构造之间 1 次 | 两次返回同一 `*Factory`；`Dump()` JSON 与 golden 完全一致 |
| 24 | `l12_generated_init` | kitex:pkg/remote/trans/nphttp2/mocks_test.go:706 | L10/L12 | 手写生成物：`rawDesc []byte`（含 0x00/0xff）+ `sync.Once` 懒压缩 + `init()` | 首次调用后 1 次 | 首次后 descriptor 非 nil、`rawDescGZIP()` 返回压缩数据且 Once 计数=1；`init` 末尾中间表置 nil；第二次同指针、计数仍=1 |
| 25 | `l12_schema_cache` | gorm:schema/schema_test.go:13 + callbacks/create_test.go:14 | L12 | 同一 struct 类型两次 Parse，传入本地 `sync.Map` | 两次 Parse 之间 1 次 | 返回同一 `*Schema` 指针；缓存 len=1；字段数/关系数与首次一致 |
| 26 | `l12_compressor_registry` | grpc-go:encoding/encoding_test.go:70 | L12 | 依次注册名为 `a`、`b`、`a` 的 3 个实现 | 注册后 1 次 | registry len=2；`a` 对应最后注册的实现；名字排序后 `[a b]` |
| 27 | `l12_event_fire_once` | grpc-go:internal/grpcsync/event_test.go:65 | L12 | 先 Fire 1 次，再顺序 Fire 3 次，最后 100 goroutine 并发 Fire 扩展 | 首次 Fire 前 1 次、全部结束后 1 次 | 首次 true；后续 103 次均 false；Done 只关闭一次；无 goroutine 泄漏 |
| 28 | `l12_pool_min_idle` | go-redis:internal/pool/pool_test.go:122 | L12 | fake dialer；`minIdle=3,maxIdle=5`；启动补连 | 补连完成后 1 次 | 初始 idle=3；Get 一个后=2；Put 回=3；Close 后=0，补连 goroutine 退出 |
| 29 | `l12_pgx_type_registry` | pgx:pgtype/pgtype_test.go:307 | L12 | 本地 Map 注册 composite 类型 OID=215333，输入 `"(1)"` | `PlanScan` 前 1 次 | `Scan` 得结构体 `{ID:1}`；Map 中 OID 可查；重复注册同名覆盖旧 codec |
| 30 | `l12_global_client_init` | go-redis:example_test.go:18 | L12 | 包级 `var ctx`/`var c`；`init()` 构造 fake client 并注册回调 | init 后、首次使用前 1 次 | client 非 nil、状态为零值；初始化回调恰 1 次；重复调用构造不重复注册 |

## 三、实现约束与坑

- **L01**：Go 不支持泛型方法（方法不能带类型参数），`RowTo[T]`/`Range[T]`/`Queue[T]` 一律用"泛型类型 + 泛型自由函数"改写；`RangeDelete` 的泛型回调参数可作为方法参数（方法本身不带类型参数）。
- **L11**：不要从 benchmark 继承 `b.N` 或计时断言；用固定 N、输出值、len/cap、指针相等性来验证优化前后语义一致。`-gcflags=-m` 的结果不是运行时契约，最多作为本地排查，不写进期望值。
- **L12**：全局注册表类用例必须使用本地 registry 或保存/恢复全局状态；`FinishInitialization` 一类"冻结"标志不可逆，单独进程跑，且该用例内不得并行。
- **GC 位置**：每条建议都要求在关键分配/收集之后调用 `runtime.GC()` 并配合垃圾分配；只靠 `GOGC` 不算。对 finalizer/池化场景，GC 后需轮询或固定等待上限，不能无限 sleep。
- **不可移植**：pgx 的 `RowTo*` 原测试全部经过 `PGX_TEST_DATABASE`；gorm 的 `G[T]` 原测试全部执行 SQL；go-redis 的 pool/example/main 测试需要 6379/16600+；grpc-go 的 registry 测试是进程级全局；kitex 的容器测试虽纯内存但依赖 `internal/test`。重写时只保留语义与状态机，不复制测试代码。
