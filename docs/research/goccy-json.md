# goccy/go-json v0.10.5

- 来源：`research/repos/goccy-json`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / `README`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/goccy-json`
- 重点维度：L05 大量分配、L07 unsafe/零拷贝、L11 编译期优化、L13 map/slice/string、L17 栈映射、L18 GC 生命周期
- 本地核验：`git describe` = `v0.10.5`，HEAD `9872089c`（2025-01-25，`fix: AnalyzeTypeAddr using sync.Once (#531)`）；`go.mod` 声明 `go 1.19`
- 模块：`github.com/goccy/go-json`；无 cgo/汇编构建标签（至少本次抓到的测试文件如此），核心是 unsafe + 反射 + 代码生成式编码器
- 测试规模：约 50 个非 `.git` 测试文件；`decode_test.go` 4059 行、`encode_test.go` 2717 行、`test/cover/*` 多为 1800–3700 行的生成式覆盖表；本次精读约 30 个文件，重点覆盖 RawMessage 生命周期、预填充清零、深度递归、流式/分块读取、指针环与 unsafe 布局
- 说明：该库测试偏“功能正确性对照 stdlib”，显式 GC/finalizer/并发压测远少于 sonic；L18/L17 场景多需从语义中提炼后用 stdlib 重新设计。

## 场景表

| 测试文件:行 | 场景摘要 | 对应维度 L01–L19 | 复杂度 低/中/高 | stdlib 重写思路 |
|---|---|---|---|---|
| `decode_test.go:2457` | `TestPrefilled`：预填充 struct/map/slice/array 后解码，验证旧值保留、超长切片截断、数组补零 | L13/L18 | 低 | `encoding/json` 同型；GC 放在解码前后，观察旧引用释放 |
| `decode_test.go:2158` | `TestUnmarshalNulls`：JSON `null` 清空预填充的指针/slice/map/interface/RawMessage | L18/L13 | 低 | 预填充后解 `null`；对旧指针挂 finalizer，GC 后验证回收 |
| `decode_test.go:2760` | `TestUnmarshalRecursivePointer`：`v = &v` 的自引用 interface 作为解码目标 | L18/L06/L17 | 低 | 同型；GC 后校验 map 值与终止性 |
| `decode_test.go:2835` | `TestUnmarshalMaxDepth`：9999/10000/300 万层嵌套，深度上限与深栈 | L03/L04/L17 | 中 | 自写递归下降解析器 + 深度计数；GC 前后各一次 |
| `decode_test.go:2947` | `TestDecodeSlice`：两次解码比较指针字段地址，验证复用 vs 重新分配 | L05/L18 | 低 | 两次 decode 到不同对象，记录 `unsafe.Pointer` 地址；GC 后比较 |
| `decode_test.go:2972` | `TestDecodeMultipleUnmarshal`：`[]json.RawMessage` → `map[string]json.RawMessage` → 再解码，逐层校验原始子串 | L07/L13/L18 | 中 | 手写 raw 子切片提取；GC 后校验子串内容与输入所有权 |
| `decode_test.go:3037` | `TestMultipleDecodeWithRawMessage`：嵌套 RawMessage 多次解码 | L07/L13 | 中 | 同上；重点验证 raw 片段边界 |
| `decode_test.go:3128` | `TestSliceElemUnmarshaler`：slice 元素实现 Unmarshaler，先解 5 个再解 1 个 | L06/L13/L18 | 低 | 自定义 `UnmarshalJSON` 元素 + 复用 slice；GC 后检查旧尾部是否释放 |
| `decode_test.go:3246` | `TestKeepReferenceSlice`：`[]interface{}{&t.A,&t.B}` 传入 Unmarshal，元素指针必须持续有效 | L18/L06/L17 | 中 | 同型；GC 期间持有元素指针，验证写屏障保留 |
| `decode_test.go:1955` | `TestLargeByteSlice`：2000 字节 `[]byte` marshal/unmarshal 往返 | L05/L07/L13 | 低 | `[]byte` base64 往返；GC 后比较相等 |
| `decode_test.go:24` | `Test_Decoder`：整型宽度、bool、string、map、slice、指针、interface 的大矩阵解码 | L06/L13/L05 | 中 | 表驱动 60+ 类型，GC 每 K 条 |
| `decode_test.go:369` | `Test_DecodeStream`：数组流式 `Token`/`More`/`Decode` | L05/L13/L19 | 低 | `json.Decoder` 同型 |
| `encode_test.go:35` | `Test_Marshal`：整型宽度、浮点、string tag、map key、指针、interface 大矩阵编码 | L06/L13/L05 | 中 | 表驱动 40+ 类型，输出与 stdlib 对照 |
| `encode_test.go:1338` | `TestSamePointerNoCycle`：两个字段指向同一对象，不得误判为环 | L13/L18 | 中 | `encoding/json` 同型；自写编码器时用“当前栈”而非全局 visited |
| `encode_test.go:1352` | `TestUnsupportedValues`：NaN/Inf、直接/间接指针环必须返回 `UnsupportedValueError` | L13/L18 | 中 | 自写编码器 + `map[uintptr]` 栈检测；GC 前后各 marshal 一次 |
| `encode_test.go:2272` | `TestRecursivePtrHead`：`[]*User` 自引用好友图（有限深）正常编码 | L18/L17 | 中 | 构造有限深指针图；与 `encoding/json` 输出对照 |
| `json_test.go:197` | `TestCompactBig`：10000 元素随机结构 JSON 压缩，字节级往返 | L05/L13/L11 | 中 | 程序生成大 JSON；`bytes.Buffer` 复用；GC 每 100 轮 |
| `json_test.go:211` | `TestIndentBig`：同一大 JSON 缩进，长度增长与幂等性 | L05/L13/L11 | 中 | `json.Indent` + `json.Compact` 往返 |
| `stream_test.go:191` | `TestDecoder`：多文档流式解码，输入去掉换行后连续 `Decode` | L05/L13/L19 | 中 | 同一 `json.Decoder` 反复 Decode；GC 间隔 |
| `stream_test.go:245` | `TestRawMessage`：RawMessage 解码后原样再编码 | L07/L13/L18 | 低 | 手写 raw 子串 + `json.RawMessage` 往返 |
| `stream_test.go:301` | `TestBlocking`：`net.Pipe` 写端 goroutine，`Decode` 不得读过头导致死锁 | L19/L02 | 中 | 同型；GC 放在 Decode 后 |
| `stream_test.go:399` | `TestDecodeInStream`：`Token`/`Delim`/`Decode` 混合的 token 流 | L13/L05 | 中 | 手工 token 序列 + 类型断言 |
| `stream_test.go:478` | `TestGzipStreaming`：400 条长字符串压缩后经 `gzip.Reader` 解码 | L05/L19 | 中 | `compress/gzip` + `json.Decoder`；GC 在解码后 |
| `stream_test.go:529` | `TestIssue278`：长字符串末尾带 `\u` 转义，贴近缓冲边界 | L05/L13 | 低 | 构造 1KB 字符串 + 尾部转义，验证不截断 |
| `path_test.go:12` | `TestExtractPath`：按 `$.a.b` 抽取原始 JSON 片段 | L07/L13 | 低 | 手写迷你路径扫描器，返回输入子切片 |
| `path_test.go:64` | `TestUnmarshalPath`：按路径直接解码到 int/bool/map | L07/L13/L06 | 低 | 路径扫描 + `json.Unmarshal` 子片段 |
| `query_test.go:38` | `TestFieldQuery`：字段查询树仅编码选中字段 | L06/L13 | 低 | 手写字段过滤 + 编码 |
| `internal/encoder/encode_opcode_test.go:8` | `TestDumpOpcode`：`unsafe` 拆 interface 头取 type 指针，查编译缓存并 dump 指令 | L07/L11 | 中 | 独立 main 可验证 interface 头布局、type 指针与 type switch，但不能导入该 internal 包 |
| `size_test.go:10` | `TestOpcodeSize`：64 位下 `unsafe.Sizeof(Opcode{})==120` | L07/L13/L15 | 低 | 用本地结构体复刻字段布局并断言 size/offset，32 位需另一组常量 |
| `benchmarks/bench_test.go:483` | `BenchmarkCodeUnmarshal`：大 code.json 并行解码到新结构体 | L02/L05/L13/L18 | 中 | 合成 code-like payload，多 goroutine 解码；GC 中间插入 |
| `benchmarks/bench_test.go:501` | `BenchmarkCodeUnmarshalReuse`：同一结构体反复解码（复用 map/slice 容量） | L05/L13/L18 | 中 | 复用目标变量，GC 前后校验最终值 |
| `benchmarks/slow_reader_test.go:36` | 慢 Reader 每次只给 64–16384 字节，解码大 payload | L05/L19 | 中 | 限长 Reader + `json.Decoder`；记录分配与结果 |
| `test/cover/cover_map_test.go:16` | 生成式覆盖：map/带 tag/omitempty/嵌套递归 map 编解码 | L06/L13 | 低 | 提炼 10 个代表性类型即可，无需搬 1900 行表 |
| `test/cover/cover_marshal_json_test.go:52` | 生成式覆盖：MarshalJSON/TextMarshaler/指针接收者组合 | L06/L13 | 中 | 提炼自定义 Marshaler 组合矩阵 |
| `number_test.go:14` | `TestNumberIsValid`：手写 JSON number 合法性校验（指数、前导零、溢出） | L13/L11 | 低 | 字节级状态机，固定用例表 |
| `encode_test.go:558` | `Test_MarshalIndent`：struct/slice/map/interface 的缩进输出与 stdlib 逐字节对照 | L05/L13 | 低 | `json.MarshalIndent` 对照，GC 每轮 |

## 推荐收录用例（按优先级排序）

### 1. `l18_prefilled_zeroing`（源 `decode_test.go:2457`）
- 输入：预填充 `struct{X float32; Y int16; Z float64}`、`map[string]any`、`[]int{1}`、`[2]int{1,2}`，再解码 `{"X":1,"Y":2}`、`[2]`、`[3]` 等。
- GC 位置：解码前 `runtime.GC()`；解码后 GC；把旧 map/slice 置空后再 GC。
- 期望：未出现字段保持旧值（Z=1.5），slice `[1]` 解 `[2,3]` 后为 `[2,3]`，数组截断/补零符合 stdlib；旧底层数组可被回收。
- 实现：`encoding/json` 同型；P2 不适合（语义型 P1）。

### 2. `l07_rawmessage_subslice`（源 `decode_test.go:2972`、`:3037`）
- 输入：2 元素数组，每元素含嵌套对象/数组；先解成 `[]json.RawMessage`，再逐层解成 `map[string]json.RawMessage`。
- GC 位置：每层解码后 GC；最后把原始 `[]byte` 置空再 GC；期望 raw 片段仍有效（拷贝语义）。
- 期望：`v["AA"]=="{\"X\":[{\"a\": \"A\"},{\"b\": \"B\"}],\"Y\":\"y\",\"Z\":\"z\"}"` 等逐字节一致；`x[0]=="{\"a\": \"A\"}"`。
- 实现：手写 raw span 扫描 + `json.RawMessage`；P2 适合（循环 1e4 次，检查不越界）。

### 3. `l05_jsonbig_compact_indent`（源 `json_test.go:197`、`:211`）
- 输入：程序生成 10000 元素嵌套 JSON（对象/数组/字符串/数字混合），约数百 KB。
- GC 位置：生成后 GC；每 100 轮 Compact 后 GC；Indent→Compact 往返后 GC。
- 期望：`Compact` 输出与原始压缩串逐字节相等；`Indent` 后长度变大且再 `Compact` 可还原；无 panic。
- 实现：`encoding/json.Compact/Indent` + 复用 `bytes.Buffer`；P2 适合。

### 4. `l18_null_clear`（源 `decode_test.go:2158`）
- 输入：预填充 struct，字段含 `*int`、`[]int`、`map[string]int`、`any`、`RawMessage`；解码全 `null` 的 JSON。
- GC 位置：预填充后对被替换的 `*int` 挂 finalizer 并 GC；解码 `null` 后 GC；再次 GC 等 finalizer。
- 期望：所有可空字段变 nil，slice/map 长度为 0 或 nil（与 stdlib 一致），旧指针被回收。
- 实现：`encoding/json` + `runtime.SetFinalizer`；P2 适合（finalizer 用有限等待，失败仅计数）。

### 5. `l17_deep_nesting_stack`（源 `decode_test.go:2835`）
- 输入：深度 9999（合法）、10000（超限）、3,000,000（深栈）的数组/对象。
- GC 位置：构造后 GC；解析后 GC；payload 释放后 GC。
- 期望：9999 层成功；10000/3M 层返回本用例自定义的最大深度错误，不崩溃。
- 实现：自写递归解析器（深度参数 `TORTURE_DEPTH`）；P2 适合。

### 6. `l05_stream_decode_loop`（源 `stream_test.go:191`、`decode_test.go:369`）
- 输入：N 个连续 JSON 文档（数字、字符串、数组、对象混合，去掉换行拼接）。
- GC 位置：每解 100 个文档 GC；解码器复用；结束后 GC。
- 期望：N 个值类型/内容与输入一致；`Token` 序列（`[`、元素、`]`）顺序正确。
- 实现：`json.Decoder` 单实例循环；P2 适合。

### 7. `l13_cycle_detect`（源 `encode_test.go:1338`、`:1352`）
- 输入：两个字段指向同一对象；自引用 `*Node` 环；`[]any` 通过 interface 包含自身。
- GC 位置：构造图后 GC；每次 marshal 前 GC；错误返回后再 GC。
- 期望：共享指针成功且只编码一份；直接/间接环返回 `*json.UnsupportedValueError`；无死循环/栈溢出。
- 实现：`encoding/json` 对照 + 自写编码器的栈式 visited 集合；P2 不适合（P0）。

### 8. `l18_recursive_pointer`（源 `decode_test.go:2760`）
- 输入：`var v any; v = &v`，解码 `{"a":"b"}` 到 `v`。
- GC 位置：构造自引用后 GC；解码后 GC；类型断言后 `KeepAlive` 再 GC。
- 期望：解码终止，`(*v).(map[string]any)["a"]=="b"`；指针链仍有效。
- 实现：纯 stdlib；P2 不适合（P1 短用例）。

### 9. `l13_slice_elem_reuse`（源 `decode_test.go:3128`）
- 输入：`[]elem`（元素实现 `UnmarshalJSON` 并记录调用），先解码 5 个元素，再解码 1 个。
- GC 位置：第一次解码后 GC；缩短 slice 后 GC；第二次解码后 GC。
- 期望：第二次 `len==1`，尾部旧元素不再可达（finalizer 或弱引用计数观察）；元素复用/重建行为与 stdlib 一致。
- 实现：自定义 Unmarshaler + 指针元素；P2 适合（循环 shrink/grow）。

### 10. `l07_path_extract`（源 `path_test.go:12`、`:64`）
- 输入：`{"a":{"b":10,"c":true},"b":"text"}`，路径 `$.a.b`、`$.b`、`$.a`。
- GC 位置：抽取后 GC；持有子切片期间 GC；输入副本释放后再 GC。
- 期望：`"10"`、`"text"`、`{"b":10,"c":true}`；子切片长度/偏移精确。
- 实现：手写迷你 JSON 路径扫描器返回 `[]byte` 子切片；P2 不适合（P1）。

### 11. `l05_parallel_code_unmarshal`（源 `benchmarks/bench_test.go:483`、`:501`）
- 输入：合成 code-like JSON（嵌套 map/slice，约 100KB，含 100+ 字段）；两种模式：每次新目标 vs 复用目标。
- GC 位置：每轮 decode 后 `runtime.GC()`；复用模式下在 decode 前 GC。
- 期望：两种模式结构校验和一致；复用模式不残留上一轮字段；无 race。
- 实现：`encoding/json` + worker pool；P2 适合（重点长压测）。

### 12. `l19_netpipe_blocking`（源 `stream_test.go:301`）
- 输入：`net.Pipe()` 写端 goroutine 一次写入 `{"x":1}` 或 `[1,2,3]`；读端 `json.Decoder.Decode`。
- GC 位置：Decode 返回后 GC；关闭 pipe 后 GC。
- 期望：不阻塞/不死锁；解析值为 map 或 slice，内容正确。
- 实现：`net.Pipe` + `json.Decoder`；P2 不适合（goroutine 调度敏感，P1）。

### 13. `l07_interface_layout`（源 `size_test.go:10`、`internal/encoder/encode_opcode_test.go:8`）
- 输入：无外部输入；本地定义 `emptyInterface{typ,data unsafe.Pointer}`，把 `any`、`*int`、`string` 装箱后拆头。
- GC 位置：装箱后 GC；拆头读取 typ/data 并 `KeepAlive` 后再 GC。
- 期望：64 位下 `unsafe.Sizeof(any)==16`，type 指针非 nil，data 与值指针一致；`switch any.(type)` 命中正确分支；不依赖具体 Opcode 120B 布局。
- 实现：纯 unsafe + reflect 对照；P2 不适合（P0/P1）。

### 14. `l05_gzip_stream`（源 `stream_test.go:478`）
- 输入：400 条长字符串数组（约 100KB），gzip 压缩后经 `gzip.Reader` 喂给 Decoder。
- GC 位置：压缩完成 GC；解码后 GC；gzip reader 关闭后 GC。
- 期望：元素数=400，首尾字符串哈希一致；解压/解码无截断。
- 实现：`compress/gzip` + `json.Decoder`；P2 适合。

### 15. `l13_token_stream`（源 `stream_test.go:399`、`:434`）
- 输入：`10`、` [10] `、对象/数组混合 token 流；`Token`、`Delim`、`Decode` 交替调用。
- GC 位置：每轮 token 序列结束后 GC；`UseNumber` 前后各 GC。
- 期望：token 类型（`json.Delim`/`float64`/`json.Number`）和顺序完全匹配；`More()` 状态正确。
- 实现：`json.Decoder` 同型；P2 不适合（P1）。

### 16. `l05_marshal_matrix`（源 `encode_test.go:35`）
- 输入：40 种类型（各宽度整型、浮点边界、`,string` tag、map 各种 key、embedded struct、Marshaler/TextMarshaler）。
- GC 位置：每轮 marshal 后 GC；map key 排序后 GC。
- 期望：输出与 `encoding/json` 逐字节一致；错误类型一致。
- 实现：表驱动 + stdlib 对照；P2 适合轻量循环（P1 为主）。

## 不可移植清单

| 测试/文件 | 不可移植原因 |
|---|---|
| `benchmarks/bench_test.go:483`、`benchmarks/slow_reader_test.go:36` | 独立模块 `module benchmark`，依赖 `jsoniter`、`segmentio/encoding`、`ffjson`、`jettison`、`fastjson`、`gojay`、`easyjson` 等外部库；partial clone 未取这些依赖，无法独立编译，只能提炼场景 |
| `internal/encoder/encode_opcode_test.go:8` | 导入 `github.com/goccy/go-json/internal/encoder`，使用未导出的 `TakeRuntimeContext`、`Opcode` 布局和运行时上下文池；`internal` 包禁止外部模块导入 |
| `size_test.go:10` | 固定断言 64 位下 `Opcode` 大小 120 字节，与实现布局和架构强绑定；32 位/实现变更即失败，不能跨 profile 复用 |
| `test/cover/cover_*_test.go`（如 `test/cover/cover_map_test.go:16`、`test/cover/cover_marshal_json_test.go:52`） | 生成式超大用例表（单文件 1800–3700 行），依赖模块自身公开 API；虽无 cgo/汇编，但体量巨大、场景重复，不适合整体搬运，只宜提炼类型组合 |
| `test/example/*_test.go` | `Example` 输出依赖模块与测试框架，不是独立 `main` 场景；且无 GC/并发压力 |
| `benchmarks/testdata/code.json.gz` | 仅数据文件；读取需解压且依赖 benchmark 模块，不能作为 `cases/` 输入直接使用 |
| `stream_test.go:445` 的 `TestHTTPDecoding`、`stream_test.go:301` 等网络/管道用例 | 本身可移植，但依赖 goroutine/网络调度时序，不适合作为确定性 `cases/` 直接运行；需改写成限时、可超时判定的版本 |

补充：goccy-json 被抓取的测试集中没有 cgo、手写汇编、`go:linkname` 或 JIT loader；不可移植性主要来自“独立 benchmark 模块的外部依赖”“internal 包导入”和“架构相关的 unsafe 布局断言”。实现包本身大量使用 unsafe（不在本次 partial clone 内），但测试层面可由 stdlib/unsafe 等价重写。
