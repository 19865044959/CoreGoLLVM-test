# sonic v1.13.2

- 来源：`research/repos/sonic`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / `README`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/sonic`
- 重点维度：L05 大量分配、L07 unsafe/零拷贝、L13 map/slice/string、L17 栈映射、L18 GC 生命周期/写屏障
- 本地核验：`git describe` = `v1.13.2`，HEAD `28294cfa`（2025-03-18，`fix(aarch64): unmarshal into json number slice (#779)`）；`go.mod` 声明 `go 1.17`
- 模块：`github.com/bytedance/sonic`，依赖 `sonic/loader`、`cloudwego/base64x`、`klauspost/cpuid`、`golang-asm`、`golang.org/x/arch` 等（这些实现文件不在 partial clone 内，用例一律以 stdlib 重写）
- 测试规模：约 175 个非 `.git` 文件，顶层 `decode_test.go`（3031 行）、`encode_test.go`（1226 行）、`search_test.go`（740 行）、`issue_test/hugestruct_test.go`（869 行）；本次精读约 35 个测试文件，重点覆盖 GC/finalizer/pool/JIT loader/AST 惰性节点/并发解码
- 构建标签提示：`decode_test.go`、`encode_test.go`、`search_test.go`、`ast/api_native_test.go`、`decoder/decoder_native_test.go` 顶部为 `(amd64 && go1.17 && !go1.25) || (arm64 && go1.20 && !go1.25)`；目标 Go 1.24.1 可编译，但 Go 1.25+ 或 386 下这些测试整体消失。多个 GC 测试带 `//go:build !race`。

## 场景表

| 测试文件:行 | 场景摘要 | 对应维度 L01–L19 | 复杂度 低/中/高 | stdlib 重写思路 |
|---|---|---|---|---|
| `internal/decoder/api/norace_test.go:33` | `CopyString` 开关：解码出的 string 是否引用输入 `[]byte`；用 `SetFinalizer` 观察输入 buffer 释放 | L07/L18/L17 | 高 | 手写最小 JSON 字段扫描器；`unsafe.String` 别名模式 vs 显式 copy 模式；finalizer + `KeepAlive` 验证存活 |
| `internal/decoder/api/norace_test.go:88` | 解码错误路径重复 `_MaxStack` 次，验证错误栈/栈溢出防护 | L03/L04/L17 | 中 | 自制递归下降解析器 + 固定错误对象，循环压深栈 |
| `internal/decoder/api/stream_test.go:162` | `HaltReader` 每次只读若干字节，流式解码后取 `Buffered` 剩余 | L05/L07/L19 | 中 | `encoding/json.Decoder` + 限长 Reader，比较值与偏移 |
| `internal/decoder/api/stream_test.go:88` | 同一 stream Decoder 连续解码多个 JSON 值，追加输入后再解码 | L13/L05 | 低 | `json.Decoder` 循环 Decode，GC 插在两次 Decode 之间 |
| `decoder/decoder_test.go:47` | 1 万 goroutine 解码 Twitter JSON 到 `interface{}`，每个 goroutine 尾部 `runtime.GC()` | L02/L05/L17/L18/L13 | 中 | `encoding/json` 解码到 `any`；校验嵌套 map/slice/string 计数与首尾字段 |
| `decoder/decoder_native_test.go:276` | CopyString/不 Copy 两模式下修改输入 buffer，断言输出字段是否随输入变化 | L07/L13/L18 | 中 | 手写字符串字段提取；别名与拷贝两种实现对照 |
| `decoder/decoder_test.go:191` | 截断/畸形 JSON 错误表（`TestDecodeCorrupt`） | L13/L06 | 低 | `json.Valid` + 自写错误位置扫描 |
| `decode_test.go:2433` | `TestUnmarshalMaxDepth`：9998 层、10000 层、300 万层嵌套数组/对象 | L03/L04/L17 | 中 | 自写深度受限扫描器；深层递归触发 morestack/栈复制 |
| `decode_test.go:2168` | `TestPrefilled`：预填充 struct/map/slice/array 后解码，验证覆盖与清零语义 | L13/L18 | 低 | `encoding/json` 同型测试；GC 放在解码前后 |
| `decode_test.go:1357` | `TestLargeByteSlice`：2000 字节 `[]byte` marshal/unmarshal 往返 | L05/L07/L13 | 低 | `[]byte`（base64）往返，GC 后比较相等 |
| `decode_test.go:1303` | `TestUnmarshalMarshal`：大类型矩阵 marshal 后 unmarshal 往返一致 | L06/L13/L05 | 中 | 30 种类型表驱动（整型宽度、float、string tag、指针、数组） |
| `encode_test.go:255` | `TestSamePointerNoCycle` / `TestSliceNoCycle`：共享指针不算环，真环报错 | L13/L18 | 中 | `encoding/json` 同型；自写编码器时用 `map[uintptr]` 记录访问 |
| `encoder/encoder_test.go:113` | `EncodeInto` 使用 cap=10 的小缓冲编码错误 JSON，缓冲不得越界 | L05/L13 | 中 | 复用 `bytes.Buffer` + 故意错误输入，检查 cap 与内容 |
| `internal/encoder/encoder_test.go:53` | map key 长度贴着 encoder 默认缓冲边界（`DefaultEncoderBufferSize-38`），触发扩容/内存破坏 | L05/L07/L13 | 中 | 构造 2KB±38 的 key 反复 marshal；GC 后 roundtrip + `json.Valid` |
| `internal/encoder/encoder_norace_test.go:30` | 1 万 goroutine 编码通用大对象，每个 goroutine 后 GC | L02/L05/L17/L18 | 中 | `json.Marshal` 循环，校验输出长度稳定 |
| `ast/parser_test.go:53` | 1000 goroutine 惰性 Parse 大 JSON，每个 goroutine 后 GC | L02/L05/L07/L17/L18 | 中 | 自制 raw-span 解析器（节点只存 input 子切片，不物化） |
| `ast/search_test.go:31` | 5000 goroutine 按路径查找节点，每个 goroutine 后 GC | L02/L07/L13/L18 | 中 | 自写路径查找，返回值引用输入子串 |
| `ast/encode_test.go:31` | 1 万 goroutine Load + `MarshalJSON`，尾部 GC | L02/L05/L18 | 中 | 节点树重建后序列化 |
| `ast/api_native_test.go:132` | `TestStackAny`：`//go:noinline` 把栈上值装箱成 `any`，GC + `FreeOSMemory` 后再序列化 | L06/L17 | 中 | noinline 返回 `any`，GC 后类型断言/编码，验证栈位图 |
| `ast/node_test.go:137` | `TestLoadAll`：惰性 raw Node 的 Load/LoadAll、nil Node、嵌套节点 | L13/L07/L18 | 低 | 自写 lazy node，raw 子切片在 Load 前不分配 |
| `ast/buffer_test.go:42` | 节点对/节点块链表在默认 cap 边界继续 Push；`Pop`/`MoveOne` 重接 | L05/L13 | 低 | 分块切片链表，超过块容量后追加新块 |
| `internal/caching/pcache_test.go:26` | 2 goroutine 并发 Compute/Get 同一 `reflect.Type` 的编译缓存 | L02/L13/L18 | 中 | `sync.Map` 或 map+RWMutex 类型缓存，GC 后仍可取到 |
| `internal/caching/hashing_test.go:50` | 通过 `unsafe.Pointer` 读 string 头逐字节计算 FNV-1a | L07/L13 | 低 | `unsafe.StringData` + 常量哈希，与 `[]byte` 循环结果比对 |
| `issue_test/issue123_test.go:28` | `TestGcWriteBarrier`：1000 goroutine 解码 10 万元素 `[]int`，并发 GC | L02/L05/L13/L18 | 中 | `encoding/json` 解码到 `[]int`；校验长度/和，P2 放大 |
| `issue_test/issue186_test.go:72` | 解码含 `[]int64` 的配置 → GC → slice 转 map → GC → 读取；1000 并发 × 100 轮 | L02/L05/L17/L18 | 高 | 同型结构 + 三次显式 GC，观察老→新引用 |
| `issue_test/issue213_test.go:34` | 1000 goroutine 解码 `*ObjStruct`（含 base64 `[]byte`） | L02/L05/L07 | 中 | 同型循环，GC 放在解码后 |
| `issue_test/issue600_test.go:28` | AST 依次 Unset 首/中节点后序列化，验证链表重接与空对象/空数组 | L13/L18 | 低 | 手写 pair/node 链表删除 + 编码 |
| `issue_test/issue634_test.go:69` | 1 万 goroutine 递归 `MarshalJSON`，内部 map key 贴着编码缓冲边界 | L02/L05/L17 | 高 | 自定义递归 Marshaler + 大 key；GC 并发 |
| `issue_test/issue762_test.go:15` | 20 goroutine × 5000 次解码含 `[][2]string` 的小对象 | L02/L05 | 中 | 同型循环；GC 每 K 次 |
| `issue_test/issue100_test.go:30` | >128B 结构体作为 map value（运行时用指针存储）解码 | L05/L13/L18 | 中 | `map[string]*Big` 与 `map[string]Big` 对照，GC 后校验 |
| `internal/decoder/optdec/native_test.go:21` | 2^20 个键的对象解析，触发缓冲重试与统计 | L05/L13 | 中 | 大对象解码进 map，GC 后校验键数；P2 默认缩到 64K 键 |
| `internal/rt/stubs_test.go:12` | 运行时 `MakeSlice` 语义：含指针/不含指针切片分配后零化 | L05/L17 | 中 | `make([]T, n)` 对照，GC 扫描含指针切片 |
| `internal/rt/pool_test.go:16` | `sync.Pool` 获取 vs 直接分配的开销对比 | L05/L18 | 低 | 去掉 benchmark，改为主程序计数 Pool 命中与分配次数 |
| `issue_test/issue115_test.go:44` | `map[int64]int`/`map[uint]int`/`map[uintptr]int` 含负数与极端大 key，排序后编码；`:76` 另测 1 万个 int key | L13/L11 | 低 | 手写 key 排序 + 编码，验证序列化顺序 |
| `issue_test/issue206_test.go:36` | 超长数字字符串解码为 `json.Number`，覆盖 1e19 等边界 | L11/L13 | 低 | `json.Number` 解析 + 字符串比较 |
| `search_test.go:291` | `TestEscapePath`：含 `.`/`\` 的 key 通过转义路径查找 | L13/L06 | 低 | 自写路径解析与转义 |
| `utf8/utf8_test.go:97` | `TestValidate_Random`：随机无效 UTF-8 字节串校验 | L13/L11 | 低 | `utf8.Valid` + 固定种子生成 |
| `issue_test/issue_recurse_test.go:16` | 自引用 `JSONTime` Marshaler 递归 marshal，验证终止 | L18/L17 | 中 | 递归 Marshaler，限制深度并校验输出 |
| `issue_test/pretouch_test.go:281` | 先小样本 decode 预热类型，再解码大对象 | L05/L11 | 低 | 预热=先解小样本；GC 后解大样本并校验 |

## 推荐收录用例（按优先级排序）

以下用例名沿用 `l<维度>_<场景>_<变体>`；“P2”一列明确是否适合长压测。

### 1. `l18_decode_gc_writebarrier`（源 `issue_test/issue123_test.go:28`）
- 输入：程序生成 `[0,1,…,99999]` 的 JSON（约 590KB）；64 个 worker，每个解码到 `[]int`。
- GC 位置：启动前 `runtime.GC()`；每个 worker 解码后立即 `runtime.GC()`；另起 1 个 goroutine 持续分配 1MB 垃圾制造并发标记。
- 期望：`len==100000`、`w[i]==i`、`sum==4999950000`；无崩溃/无错误。
- 实现：`encoding/json.Unmarshal`；P2 适合（worker 可放大到 512，`TORTURE_ROUNDS` 控制轮数）。

### 2. `l07_copystring_alias_finalizer`（源 `internal/decoder/api/norace_test.go:33`）
- 输入：字面量 `{"A":"0","B":"1"}` 存放在 `[]byte`；手写扫描器分别产生“别名 string”和“拷贝 string”。
- GC 位置：对输入首字节 `SetFinalizer` 后 `runtime.GC()`；解码并持有别名串时再 GC，finalizer 不得执行（原子标志）；drop 别名串后循环 GC 至 finalizer 执行。
- 期望：两种模式都得到 `A=="0"`/`B=="1"`；别名字符串在输入被改写后观察到变化，拷贝模式不变。
- 实现：`unsafe.String(unsafe.SliceData(buf[i:j]), n)`；P2 不适合（finalizer 时序不确定，做 P1 短用例；别名语义本身确定性可测）。

### 3. `l05_decode_interface_churn`（源 `decoder/decoder_test.go:47`）
- 输入：生成 200 个对象的 JSON（每对象 10 字段，含嵌套数组/字符串），约 200–500KB；1000–10000 goroutine 解码到 `any`。
- GC 位置：每个 goroutine 解码后 `runtime.GC()`；主 goroutine 每 100 次额外 `runtime.GC()`。
- 期望：每份结果对象数=200，`arr[0].id`、`arr[199].name` 固定；map/slice/string 嵌套计数一致。
- 实现：`encoding/json`；P2 适合。

### 4. `l05_encoder_scratch_boundary`（源 `internal/encoder/encoder_test.go:53`、`encoder/encoder_test.go:113`）
- 输入：嵌套 map，key 长度取 `2048-38`、`4096-38`、`8192+5` 三档，value 为 `int32`；1000 次 marshal/unmarshal。
- GC 位置：每轮 `runtime.GC()` 前后各分配一块垃圾；错误路径用 cap=10 的复写缓冲。
- 期望：输出均为合法 JSON，roundtrip 后 key/value 完全一致，小缓冲 `cap` 不被越界写入。
- 实现：`encoding/json.Marshal` + `bytes.Buffer`；P2 适合（提高轮数）。

### 5. `l17_stack_iface_boxing`（源 `ast/api_native_test.go:132`）
- 输入：无外部输入；`//go:noinline` 函数把局部 `int`/`*struct` 装箱成 `any` 返回。
- GC 位置：拿到 `any` 后 `runtime.GC()` + `debug.FreeOSMemory()`，再类型断言并 `json.Marshal`。
- 期望：断言值正确（如 `1`），序列化输出 `1`；GC 扫描到的是正确的栈槽位图。
- 实现：纯 stdlib；P2 不适合（P0 短用例，循环 1e4 次即可）。

### 6. `l18_reload_slice_to_map`（源 `issue_test/issue186_test.go:72`）
- 输入：5 元素 JSON 数组，元素含 `[]int64`（长度 1–5）和短字符串；1000 goroutine × 100 轮。
- GC 位置：解码前 GC；解码后 GC；用 slice 构造 `map[int64]bool` 后再 GC；读取 map 前再 GC。
- 期望：每轮 5 个元素；map 大小分别为 1/1/1/1/5；`tmp[0].A=="xxx"`。
- 实现：同型 struct + `encoding/json`；P2 适合（减到 100×20 可作 P1）。

### 7. `l17_deep_nesting_stack`（源 `decode_test.go:2433`）
- 输入：深度 9998（合法）、10001（超限）、3,000,000（深栈）三层数组/对象。
- GC 位置：构造 payload 后 GC 一次，解析后 GC 一次，释放 payload 再 GC。
- 期望：9998 层成功；10001/3M 层返回确定的最大深度错误，不 panic、不栈溢出。
- 实现：自写递归下降解析器 + 深度计数（错误文本由用例定死，不依赖三方库）；P2 适合（`TORTURE_DEPTH` 放大）。

### 8. `l13_ast_unset_links`（源 `issue_test/issue600_test.go:28`）
- 输入：手写节点链：对象 `{x:a,y:b}` 依次删除 x、y；数组 `[1,2]` 依次删除下标 1、0。
- GC 位置：每次 Unset 后 GC，最后一个节点 Marshal 前再 GC；被删节点挂 finalizer 观察最终回收。
- 期望：对象输出 `{}`，数组输出 `[]`；剩余链长度/顺序正确。
- 实现：手写 pair/node 链表与删除逻辑；P2 不适合（P1，规模小）。

### 9. `l05_large_map_ptr_values`（源 `issue_test/issue100_test.go:30`）
- 输入：9 个 key 的 JSON 对象，value 是 `{Pad [160]byte; S string}`（>128B，map bucket 存指针）。
- GC 位置：解码后 GC；再次覆盖解码后 GC；读取/比较前后各 GC。
- 期望：`len(map)==9`，9 个 value 指针互不相同，`S` 与 JSON 对应；覆盖后值更新。
- 实现：`map[string]*Big` 与 `map[string]Big` 两种对照；P2 适合。

### 10. `l07_ast_lazy_raw_nodes`（源 `ast/node_test.go:137`、`ast/parser_test.go:53`）
- 输入：50KB 嵌套 JSON；解析成惰性节点，节点只保存输入的子切片/子串，不拷贝。
- GC 位置：解析后 GC；`Load` 第一层后 GC；`LoadAll` 后 GC；Marshal 前再 GC。
- 期望：路径值、数组长度、对象 key 均正确；raw 子串长度与输入区间一致；输入 buffer 在 `runtime.KeepAlive` 前不被回收。
- 实现：手写 raw-span 解析器 + `unsafe.String`；P2 适合（可循环 1e4 次）。

### 11. `l13_mapkey_sort_reuse`（源 `issue_test/issue115_test.go:44`、`:76`）
- 输入：`map[int64]int{-2:2,0:0,1:-1}`、`map[uint]int{0:0,1:-1,^uint(0):2}`、`map[uintptr]int{0:0,0xf:0xf}`；P2 变体另加 10000 个连续 int key。
- GC 位置：map 构造完成 GC；marshal 后 GC；unmarshal 回 map 后再 GC。
- 期望：输出 `{"-2":2,"0":0,"1":-1}`，uint/uintptr 大 key 按十进制排序；回读 map 相等。
- 实现：`encoding/json` 天然排序；P2 适合（1 万 key 变体可作 P1）。

### 12. `l02_concurrent_decode_small`（源 `issue_test/issue762_test.go:15`）
- 输入：常量 `{"Arr":[["foo","bar"]]}`；20 goroutine × 5000 次解码到 `struct{Arr [][2]string}`。
- GC 位置：每 worker 每 100 次 `runtime.GC()`；主 goroutine 每 1000 次 GC。
- 期望：每次 `Arr[0][0]=="foo"`、`Arr[0][1]=="bar"`；无错误。
- 实现：`encoding/json`；P2 适合。

### 13. `l05_parse_million_keys`（源 `internal/decoder/optdec/native_test.go:21`）
- 输入：默认 65536 个互不相同的 `"kN":N` 键对象（P2/TORTURE 放大到 2^20），再穿插 `null` 值变体；源测试用重复键 `"a"` 是为了压缓冲重试，改写成独立用例时改用唯一键以便校验。
- GC 位置：payload 构造后 GC；解析完成 GC；map 置空后再 GC，观察大对象回收。
- 期望：`len(m)==N`，全部 value 正确；无重试死循环。
- 实现：`json.Unmarshal` 到 `map[string]int`；P2 适合（默认 64K 键控制在秒级）。

### 14. `l13_node_chunk_linked`（源 `ast/buffer_test.go:42`、`:90`、`:180`）
- 输入：向 pair/node 块链表 Push 1000 个节点（超过默认块容量），随后 Pop 500、MoveOne 若干次。
- GC 位置：超过第一个块容量时 GC；Pop 后 GC；ToSlice 前 GC。
- 期望：长度、顺序、移动后的位置全部正确；不出现悬挂 next 指针。
- 实现：分块切片链表；P2 不适合（P1）。

### 15. `l18_pcache_concurrent`（源 `internal/caching/pcache_test.go:26`）
- 输入：共享缓存 `map[reflect.Type]any`；8 goroutine 各 1000 次 Compute/Get 同一类型；每轮分配临时 map。
- GC 位置：Compute 与 Get 并发期间主 goroutine 周期性 `runtime.GC()`，强制 hash bucket 扫描。
- 期望：所有 Get 返回同一描述符（指针相等），缓存条目数=1，无丢写。
- 实现：`sync.Map` 或 `map+sync.RWMutex`；P2 适合。

### 16. `l07_unsafe_string_hash`（源 `internal/caching/hashing_test.go:50`）
- 输入：固定字符串集合（含中文/长 key）；分别用 `unsafe.StringData` 与 `[]byte` 计算 FNV-1a。
- GC 位置：两轮哈希之间 GC；字符串来自全局常量，验证不拷贝。
- 期望：两种算法结果相等且等于预置常量；不读越界。
- 实现：`unsafe.StringData` + 逐字节循环；P2 不适合（P0）。

### 17. `l18_stream_chunked_decode`（源 `internal/decoder/api/stream_test.go:88`、`:162`）
- 输入：3 个连续 JSON 文档（含 4KB 字符串）；自定义 Reader 每次最多返回 1/3/7/17 字节，并随机返回 `(0,nil)` 半关闭。
- GC 位置：每解完一个文档 GC；Reader 每次返回后分配垃圾；末尾 `Buffered` 读取后 GC。
- 期望：3 个文档顺序/内容正确；`InputOffset` 单调；无死锁。
- 实现：`encoding/json.Decoder` + 限长 Reader；P2 适合。

## 不可移植清单

| 测试/文件 | 不可移植原因 |
|---|---|
| `internal/native/sse/native_test.go:39`、`internal/native/sse/recover_test.go:52`；`internal/native/avx2/*`；`internal/native/neon/*` | 手写汇编函数（SSE/AVX2/NEON），依赖 ABI0/ABIInternal 包装、平台 build tag（amd64/arm64）和 sonic native 目标文件；无法在独立 `main` 中链接 |
| `internal/decoder/jitdec/assembler_test.go:679`、`internal/decoder/jitdec/compiler_test.go:26` | JIT 汇编器/编译器，需要 `golang-asm` 生成机器码、可执行内存与 sonic internal 运行时；部分用例只在 amd64/arm64 且 Go 1.17–1.24 下编译 |
| `internal/encoder/x86/assembler_test.go:41` | `ForceUseJit` 后用 x86 汇编器加载机器码，传裸 `unsafe.Pointer` 和自定义栈；架构/ABI 绑定 |
| `loader/wrapper_test.go:49` | 直接嵌入机器码字节、手工填 `Pcsp` 栈映射表并调用 `WrapGoC` 注册；依赖运行时 moduledata 与可执行 mmap，属于 JIT/内部 linkname 类能力 |
| `loader/loader_go117_test.go:33` | 自定义模块加载器（mmap、符号注册、`go1.17+` 构建标签），依赖 sonic loader internal 运行时 |
| `loader/internal/iasm/x86_64/instructions_test.go:25`、`loader/internal/iasm/x86_64/program_test.go:27` | x86-64 汇编/指令编码测试，仅限该架构，且依赖 loader 内部包 |
| `issue_test/plugin_test.go:65` | 通过 `go build -buildmode=plugin` 生成 `.so` 再用 `plugin.Open` 加载；需要 cgo/外部链接、Go 工具链和 Linux/Unix 环境，CoreGoLLVM 独立编译不可用 |
| `internal/rt/fastconv_test.go:14`、`internal/rt/stubs_test.go:12` | 依赖 sonic 的 `rt` 内部布局（`GoString`/`GoSlice`/`Gface`、`UnpackType`、`MakeSlice`），部分路径最终落到 runtime 内部；可提炼场景但不能直接搬进 `package main` |
| `internal/encoder/vm/vm_test.go`、`internal/encoder/compiler_test.go:28` | 导入 `github.com/bytedance/sonic/internal/encoder`，使用未导出的 VM/编译器上下文；`internal` 包禁止外部模块导入 |
| `decode_test.go`/`encode_test.go`/`search_test.go`/`decoder/decoder_native_test.go` 顶部 build tag | 仅 `(amd64 && go1.17 && !go1.25)` 或 `(arm64 && go1.20 && !go1.25)` 生效；Go 1.25+、386 等 profile 下整文件不编译，不能作为跨 profile 用例直接复用 |
| `internal/decoder/api/norace_test.go`、`internal/encoder/encoder_norace_test.go`、`issue_test/issue634_test.go` 的 `//go:build !race` | 显式排除 race profile；其中的 unsafe/裸指针场景不能原样在 `-race` 下验证 |
| `testdata/*.json.gz` | 需 gzip 解压且体积大；只能作为场景/数据形态参考，用例应改为程序内确定性生成 |
| `issue_test/pretouch_test.go:281`、`issue_test/issue100_test.go:30` 等大量 `issue_test` | 直接依赖 `sonic`/`sonic/ast` 的公开 API；场景可移植，但代码不能独立编译，需按 stdlib 重写 |

补充：`internal/rt/pool_test.go` 本身只是 benchmark，无断言；`issue_test/plugin_test.go` 会派生 `go build` 和 `rm` 子进程，运行环境依赖重，亦不适合收进 `cases/`。
