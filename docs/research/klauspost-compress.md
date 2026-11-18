# klauspost/compress v1.17.11

- 来源：`research/repos/klauspost-compress`（partial clone，仅 `*_test.go`、`testdata/`、`go.mod`、`README`）
- 规模：61 个测试文件，80MB（绝大部分为 testdata），go.mod `go 1.21`，HEAD `72cd4a9`（zstd: Fix extra CRC written with multiple Close calls）
- 覆盖包：`flate`、`gzip`、`zlib`、`zip`、`zstd`、`s2`、`snappy`、`huff0`、`fse`、`gzhttp`、根包 `compressible`
- 说明：本仓为压缩算法库，L05/L07/L08/L11/L13 信号最强；L02 并发集中在 zstd/gzhttp/zip；L03 有超长稀疏流用例；L18 来自池化、Writer/Decoder Reset 与 Scratch 复用。测试大量依赖库自身的压缩格式与手写汇编路径，stdlib 重写只能借鉴“场景与不变量”，不能复现格式与汇编实现。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| gzip/gzip_test.go:46 | Writer 写入 Name/Comment/Extra/ModTime，Reader 原样读回 | L13 | 低 | `compress/gzip` Header 字段 + 固定时间，字符串逐项比较 |
| gzip/gzip_test.go:166 | 空 Flush 只写头部；写入 1 字节后未 Flush 不增长；再次 Flush 才输出 | L13 | 低 | `gzip.Writer.Flush`，按输出长度分三段断言 |
| gzip/gzip_test.go:207 | 两个独立 gzip member 顺序拼接，读取为“hello world” | L13 | 低 | stdlib gzip 默认 Multistream 行为 |
| gzip/gzip_test.go:226 | 同一 Writer Reset 到第二个 buffer，输出与首轮逐字节相同 | L05/L18 | 低 | `gzip.Writer.Reset` 循环，穿插 `runtime.GC()` 检查复用正确性 |
| gzip/gzip_test.go:408/417 | 1KB/100KB/1MB/10MB 重复 JSON 压缩后解压一致 | L05 | 中 | stdlib gzip 多尺寸往返，记录 `MemStats`，大尺寸标 P2 |
| gzip/gzip_test.go:581 | 每轮新建 Writer 压缩小载荷（level -2..9）的分配次数 | L05/L11 | 低 | `testing.AllocsPerRun` 包裹 stdlib gzip；验证逃逸/内联敏感性 |
| gzip/gunzip_test.go:471 | `Multistream(false)` 逐 member 读取，Reset 到下一 member，最后 EOF | L13 | 低 | `gzip.Reader.Multistream(false)` + Reset 两轮 |
| gzip/gunzip_test.go:585 | 对同一 gzip 流取 1..len-2 的每个前缀，必须返回 `ErrUnexpectedEOF` | L13 | 低 | stdlib gzip 截断表驱动；覆盖头部/块/尾部三种截断位置 |
| gzip/gunzip_test.go:733 | bufio 读到换行后再 `io.Copy` 剩余部分，跨越 deflate 块边界 | L13/L05 | 低 | bufio.Reader 包 stdlib gzip.Reader，校验剩余内容 |
| flate/inflate_test.go:17 | 一个 Reader 先后 Reset 到两个 deflate 流并解压 | L13/L05 | 低 | stdlib `flate.Resetter.Reset` 两次 |
| flate/inflate_test.go:45 | 10 组截断/非法输入：返回 `ErrUnexpectedEOF` 且保留已解出前缀 | L13 | 低 | stdlib flate 对每组字节前缀解压，比对错误与前缀 |
| flate/inflate_test.go:284 | `WithPartialBlock` 读取只含部分块的流，读出 "hello, world" | L13 | 中 | stdlib 无此 API；仅能改写成截断流行为，或在自写块读取器中模拟 |
| flate/writer_test.go:22 | 各 level + StatelessDeflate 的 heap 增量/对象数，并与 stdlib 对照 | L05/L11 | 中 | `runtime.MemStats` 前后采样 + stdlib flate 各 level；断言上界而非精确值 |
| flate/deflate_test.go:156 | 2.3GB 稀疏流压缩（short 模式跳过），覆盖长循环与超大输入 | L03/L05 | 高 | 自写 sparseReader + stdlib flate.Writer，P2；每轮 `runtime.GC()` |
| flate/writer_test.go:327 | 各 level 写 512KB 后 Reset，再验证窗口/token 状态复位 | L05/L18 | 中 | stdlib `flate.Writer.Reset` 循环，输出可重复且无残留 |
| flate/writer_test.go:395 | level -2..9 跨 30 个块边界，两次运行输出逐字节一致 | L11 | 低 | stdlib flate 全 level 双跑比对；编译器 codegen 偏差会直接暴露 |
| flate/token_test.go:28 | token 估算位数固定为 221057，并与动态块实际位数对照 | L11 | 低 | 自写 token/位计数模型，固定期望值 |
| huff0/compress_test.go:272 | 只用前次 Huffman 表重压同一数据，必须复用且输出一致 | L05/L18 | 中 | 自写可复用 Scratch/表结构，二次压缩校验复用标志 |
| huff0/compress_test.go:433 | 255 个 128KB 块连续压缩；第一块建表，其后必须复用 | L05/L18 | 中 | 循环复用直方图/表，期间分配垃圾并 GC |
| s2/s2_test.go:93 | 汇编 Encode 与纯 Go encodeGo 交叉验证；源 slice 不得被改；拼接块带前缀 | L08/L13 | 中 | 汇编部分不可移植；可移植部分：拼接/前缀/源不可变语义用 stdlib 压缩重写 |
| s2/s2_test.go:161 | dst 容量 nil/20/64 三档 × 32 种长度，覆盖扩容与小块路径 | L13/L05 | 低 | stdlib flate 配小容量/复用 buffer，校验增长后内容一致 |
| s2/s2_test.go:174 | 随机输入长度 1..20000（步长 23），种子固定 | L05/L13 | 低 | 固定 seed + stdlib 编解码往返；失败输出种子 |
| s2/s2_test.go:493 | length/offset 1..18 组合解码，不得写到 `len(dst)` 之外（容量内哨兵） | L07/L13 | 中 | 自写 LZ copy 解码或 stdlib 解码，dst 用 `[:n:n]` + 哨兵字节检测越界写 |
| s2/s2_test.go:611 | 纯 Go 模拟汇编 “expand pattern” 算法，验证越界写不超过 10 字节 | L08/L11 | 低 | 算法本身可纯 Go 重写；验证 1..17 的 length/offset 组合不变量 |
| s2/s2_test.go:655 | 压缩流中噪声/可压块交替时 Skip 各种跨度后继续读取 | L13 | 中 | 基于 stdlib gzip 实现逻辑 skip（读并丢弃），校验剩余内容 |
| s2/s2_test.go:727 | 前半随机、后半重复的 256KB/2MB 数据，编码后应小于 75% | L11 | 低 | stdlib flate 压缩率上界断言，验证算法路径未被编译器改坏 |
| s2/s2_test.go:1093 | Writer Reset 20 次、每次写入不同截断长度，所有输出均需正确 | L05/L18 | 中 | stdlib gzip/flate Writer.Reset 循环；缓存输出逐个回解 |
| s2/s2_test.go:1133 | 未 Flush 直接 Reset 时，上一段未完成数据必须丢弃 | L13 | 低 | stdlib gzip Reset 语义对照 |
| s2/s2_test.go:2165 | matchLen 优化实现与逐字节参考实现在 30..40 边界附近对比 | L11/L13 | 低 | 自写 matchLen + 参考实现，允许末尾少量 shortening |
| s2/writer_test.go:475 | 连续 10 次写 2 个块大小（约 256KB）的整块缓冲 | L05 | 中 | stdlib flate 按 128KB/256KB 写入，校验总量与内容 |
| s2/writer_test.go:505 | 同一 2MB buffer 连续 EncodeBuffer 两次（未修改），不得复制/改变输入 | L07/L05 | 中 | 契约测试：Write 后修改源 buffer，验证输出不随之变化；同步路径可复用 |
| s2/writer_test.go:546 | WriterConcurrency(1) 时 EncodeBuffer 返回即可复用输入 buffer | L07 | 中 | 串行 Writer 立即消费输入的等价模型，不保留 slice 引用 |
| s2/writer_test.go:579 | BufferDone 回调按 10B/100B/10KB/1MB/8MB 归还 buffer，并写满检测竞态 | L07/L18 | 中 | sync.Pool 分尺寸 Get/Put + 回调后改写内容，验证无别名/重复归还 |
| s2/encode_test.go:14 | MaxInt32、MaxInt32+64K、MaxBlockSize 的超大 block 编解码（默认跳过） | L05 | 高 | 按 P2 缩到 256MB/1GB 或仅在 TORTURE 下跑；验证 MaxEncodedLen 上界 |
| s2/fuzz_test.go:16 | 编码目的缓冲写满精确 cap，cap 之后 4 字节哨兵不得被修改 | L07/L13 | 中 | 用 `dst[:max:max]` + 尾部哨兵；stdlib flate 同样检测越界写 |
| snappy/snappy_test.go:412 | length/offset 1..18 × suffix 0..18，解码不得越界写、不得读不存在的 literal | L07/L13 | 中 | 自写简版 copy 解码；容量哨兵检测 |
| snappy/snappy_test.go:530 | 与 s2:611 同源的汇编 overrun 模型 | L08 | 低 | 纯 Go 算法不变量 |
| snappy/xerial/xerial_test.go:44 | 约 13MB 的重复长文本一次性 Encode/Decode | L05/L13 | 中 | stdlib gzip 压缩同尺寸字符串，校验与内存峰值 |
| zstd/encoder_test.go:107 | 共享 Encoder，~400/GOMAXPROCS 个 goroutine 并发 EncodeAll + DecodeAll | L02/L05 | 中 | 每 goroutine 独立 gzip.Writer 或用 sync.Pool；WaitGroup + 长度/内容校验 |
| zstd/encoder_test.go:827 | Write/Write/ReadFrom/Write 交错，解压后顺序必须完全一致 | L13 | 低 | stdlib gzip Writer 的 Write 与 ReadFrom 交错序列 |
| zstd/decoder_test.go:222 | 创建 5000 个 Decoder，各读 16KB，统计每 decoder 堆占用 | L05/L18 | 高 | stdlib gzip.Reader 实例数组 + `MemStats`，P2；断言上界 |
| zstd/decoder_test.go:847 | Decoder Reset 后重复解码，底层内存复用且结果一致 | L05/L18 | 中 | stdlib gzip.Reader.Reset 循环 |
| zstd/decoder_test.go:914/967 | 多帧 2x/4x/8x 拼接；Reset 后随机读一部分再 Reset 重读全量 | L13 | 中 | gzip Multistream + Reset，随机长度由固定种子驱动 |
| zstd/decoder_test.go:2169 | DecodeAll cap 从 0 到 1MB 的 2 的幂，目标已有 5 字节前缀 | L13/L07 | 中 | stdlib 解码到 `dst[:existingLen:cap]`，校验 ErrShortBuffer/append 语义 |
| zstd/zstd_test.go:66 | 10MB 数据 ×100 次 EncodeAll，并发 32，统计 heap/对象数 | L05 | 高 | stdlib gzip 同尺寸循环，P2 |
| zstd/zstd_test.go:45 | 129 字节序列逐位翻转，校验 matchLen 边界 0..128 | L11 | 低 | 自写 matchLen 参考实现 |
| zstd/seqdec_test.go:17 | 三个 prevOffset 与 offset/litLen/offsetB 组合的 adjustOffset 表 | L11/L13 | 低 | 纯算术模型，固定表驱动 |
| zstd/seqdec_test.go:199 | 从 bitstream 解码 FSE 序列并与 CSV golden 对比 | L11/L13 | 高 | 自写 bit reader/FSE 模型，P2 |
| zstd/seqdec_amd64_test.go:30 | 关闭 BMI2 后走通用路径，与汇编路径结果对比 | L08 | 高 | 不可移植；仅纯 Go 位运算模型可借鉴 |
| zstd/internal/xxhash/xxhash_test.go:138 | Sum64/Digest 必须 0 分配 | L05/L11 | 低 | `testing.AllocsPerRun` 包自写 hash；验证内联/逃逸 |
| zip/zip_test.go:22 | 65578 个 entry 创建、关闭、再解析，名称顺序完整 | L13/L05 | 中 | `archive/zip` Store 模式写 65578 个空文件，校验 File 数量和顺序 |
| zip/zip_test.go:502 | 中央目录逼近/超过 uint32max，触发 zip64 分支 | L05/L13 | 高 | P2；archive/zip 大目录写读，验证边界切换 |
| zip/writer_test.go:300 | Flush 前缓冲区为空，Flush 后本地头立即输出 | L13 | 低 | archive/zip Writer.Flush 长度断言 |
| zip/writer_test.go:629 | 并行循环，每次写 3 个 1MB deflate entry，制造 GC 压力 | L02/L05/L18 | 中 | 每 goroutine 独立 archive/zip Writer；`b.RunParallel` 风格 |
| gzhttp/compress_test.go:1020 | 响应随机 padding 256..1024；9 次请求长度应有变化且不小于基准 | L13/L05 | 中 | net/http handler + stdlib gzip；固定种子校验 padding 边界 |
| gzhttp/writer/gzkp/gzkp_test.go:9 | 双层 Close 不得把同一 Writer 两次放回 pool | L05/L18 | 低 | sync.Pool + 双重 Close 守卫，Get 两次必须不同对象 |
| compressible_test.go:9 | 零/随机/base32/文本四类数据 500B..500KB 的熵估算 | L11/L13 | 低 | 自写字节直方图 + Shannon 熵，固定输出范围 |
| fse/fse_test.go:106 | 损坏流（hang/crash/symlen）必须报错，解压上限 1MB | L13 | 中 | stdlib flate 处理损坏前缀，校验不 panic、不超限分配 |

## 推荐收录（18 条）

1. `l05_gzip_writer_reset_churn`：gzip/gzip_test.go:226。L05/L18，P1。stdlib gzip Writer.Reset 循环 20 次、载荷长度递减；每轮分配垃圾并 `runtime.GC()`；缓存每轮输出后统一回解，校验 20 份结果。用于验证 Reset 后内部窗口/哈希表/写缓冲复用是否正确。
2. `l05_gzip_levels_large_roundtrip`：gzip/gzip_test.go:408/417。L05，P1。1KB/100KB/1MB/10MB 四档重复 JSON；stdlib gzip 默认与 BestSpeed 两档；校验字节一致、`MemStats` 有界。10MB 档支持 `TORTURE_SCALE`。
3. `l05_flate_writer_heap`：flate/writer_test.go:22。L05/L11，P1。对每个 level 新建 stdlib flate.Writer 写 100KB，采样 `HeapInuse/HeapObjects`；断言相对上界而非绝对值，避免与 GC 时序耦合。
4. `l05_flate_sparse_huge`：flate/deflate_test.go:156。L03/L05，P2。自写 sparseReader 产出 2.3GB 稀疏字节，`io.Copy` 到 stdlib flate.Writer；每 256MB 调 `runtime.GC()`；验证长循环安全点、超大输入与输出可解。
5. `l05_zstd_conc_encode`：zstd/encoder_test.go:107。L02/L05，P1。N=400/GOMAXPROCS 个 goroutine，各自从共享 1MB 源按随机长度切片压缩+解压；stdlib 等价为每 goroutine 独立 gzip.Writer（或 sync.Pool 复用），WaitGroup 汇合。重点覆盖并发压缩下的堆分配与 GC。
6. `l05_zstd_decoder_instances`：zstd/decoder_test.go:222。L05/L18，P2。创建 5000 个 stdlib gzip.Reader，各读 16KB；采样 heap 前后差值；验证实例级缓冲的固定开销与回收。
7. `l05_huff0_scratch_reuse`：huff0/compress_test.go:433。L05/L18，P1。自写可复用 scratch（直方图表/输出 slice），连续处理 255 个 128KB 块；第一块分配表、其后复用；期间制造垃圾并 GC，校验输出不串块。
8. `l05_zip_concurrent_garbage`：zip/writer_test.go:629。L02/L05/L18，P1。并行 8 个 goroutine，每个循环写 3 个 1MB deflate entry；stdlib archive/zip + bytes.Buffer；强制 GC，验证并发堆压力与写入正确性。
9. `l05_xxhash_zero_alloc`：zstd/internal/xxhash/xxhash_test.go:138。L05/L11，P0。`testing.AllocsPerRun(10, ...)` 要求 0 分配；自写短字符串 hash 与 Digest 两版，是逃逸分析/内联最敏感的 P0 用例。
10. `l05_pool_double_close`：gzhttp/writer/gzkp/gzkp_test.go:9。L05/L18，P0。sync.Pool 存 `*gzip.Writer`；Close 两次后 Get 两次必须得到不同对象；验证池不能重复归还同一指针。
11. `l07_s2_encode_buffer_owner`：s2/writer_test.go:505/546。L07/L05，P0。写 2MB buffer 后立刻改写源数据（或连续写两次），验证 stdlib gzip 输出不引用调用方缓冲；串行 Writer 场景下模拟“返回即复用”。
12. `l07_s2_buffer_done_pool`：s2/writer_test.go:579。L07/L18，P1。按 10B/100B/10KB/1MB/8MB 五档从 sync.Pool 取缓冲交给压缩，回调归还时校验尺寸与内容，并在回调内写满检测别名/竞态。
13. `l07_decode_cap_guard`：s2/s2_test.go:493 + s2/fuzz_test.go:16 + zstd/decoder_test.go:2169。L07/L13，P0。目标统一用 `dst[:n:n]`，cap 尾部放哨兵；分别覆盖 LZ copy 解码、编码器输出、DecodeAll 前缀+cap 边界（0..1MB 的 2 的幂）。校验不越界写、不错误复用 cap。
14. `l07_snappy_decode_overrun`：snappy/snappy_test.go:412。L07/L13，P1。length/offset/suffix 三重边界组合（1..18），解码后检查 `dst[len:cap]` 哨兵未被修改；可用自写 copy 解码器承载。
15. `l08_copy_overrun_model`：s2/s2_test.go:611（snappy/snappy_test.go:530 同源）。L08/L11，P0。纯 Go 重写“expand pattern”模拟，验证 1..17 组合的 overrun 落在 [0,10]；这是本库唯一可移植的 L08 场景，验证的是汇编算法不变量而非真实 SIMD。
16. `l11_deterministic_levels`：flate/writer_test.go:395（gzip/gzip_test.go:429 同型）。L11，P0。stdlib flate 全 level，对 30 个块边界长度的可压数据双跑，逐字节相等；内联/常量传播/边界检查消除一旦出错会直接改变码流。
17. `l11_matchlen_boundary`：s2/s2_test.go:2165 + zstd/zstd_test.go:45。L11/L13，P0。自写 matchLen 优化版与逐字节参考版，在 30..40 与 0..129 边界对比；优化实现允许末尾少量 shortening。
18. `l13_small_capacity_roundtrip`：s2/s2_test.go:161/174。L13/L05，P0。nil/20/64 三档目标容量 × 32 种输入长度，再加固定种子随机长度 1..20000；stdlib flate 编解码；验证 append/扩容路径与切片别名。

补充可选（超出 18 条）：`zip/zip_test.go:22` 65578 entry（L13/L05，P1）；`snappy/xerial/xerial_test.go:44` 13MB 文本（L05/L13，P1）；`compressible_test.go:9` 熵估算（L11/L13，P0）；`flate/deflate_test.go:156` 2.3GB 稀疏流作为 P2 长压测。

## 不可移植清单

1. **amd64 BMI2 汇编路径**：`zstd/seqdec_amd64_test.go:30`、`zstd/fuzz_test.go:57`、`zstd/fuzz_test.go:119`。构建标签 `amd64 && !appengine && !noasm && gc`，运行时用 `cpuinfo.HasBMI2()` / `DisableBMI2()` 切换汇编与通用路径。CoreGoLLVM 用例不能依赖该库，也无法触发其 BMI2 代码；只能借鉴“同一算法两条实现结果必须一致”的思路，用纯 Go 位运算模型验证序列解码语义。
2. **s2/snappy 汇编与纯 Go 交叉验证**：`s2/s2_test.go:93`（`Encode`/`EncodeBetter` 汇编路径 vs `encodeGo`）、`s2/s2_test.go:161/174`（经 `roundtrip` 间接走汇编）、`s2/s2_test.go:1495/1947`（noasm 基准对照）。partial clone 不含 `.s`，stdlib 也没有 s2/snappy 编码器。可验证的等价行为：压缩往返、源 slice 不被修改、拼接块/前缀语义、小缓冲容量路径；不能验证汇编正确性或 SIMD 指令。
3. **汇编 overrun 模型**：`s2/s2_test.go:611`、`snappy/snappy_test.go:530`。测试注释明确说验证 `decode_amd64.s` 的 10 字节 overrun 假设；逻辑本身是纯 Go，因此代码可移植，但它验证的是汇编实现的边界假设，不是 stdlib 行为。若收录，标注为“L08 等价模型”。
4. **snappy/s2 定制 bitstream**：`snappy/snappy_test.go:382/412`、`s2/s2_test.go:463/493`、`zstd/seqdec_test.go:199`、`fse/fse_test.go:106`。这些输入是 snappy/zstd/FSE 私有格式，stdlib 无对应解码器；可移植的是“长度/偏移边界、禁止越界写、损坏流不 panic”的通用不变量，需换成 stdlib flate/gzip 的对应场景，或自写简化 copy 解码器。
5. **zstd 私有格式与词典**：`zstd/dict_test.go:14` 及 zstd 大部分用例依赖 zstd frame/字典格式，stdlib 无 zstd。字典压缩语义可用 `compress/flate.NewWriterDict` + `Resetter.Reset(r, dict)` 或 `compress/zlib.NewWriterLevelDict` 验证“共享字典可提高压缩率、解码必须带同字典”，但无法复现 zstd 字节流。
6. **flate 私有 partial-block API**：`flate/inflate_test.go:284` 使用 `WithPartialBlock()`，stdlib `compress/flate` 无对应入口。可移植替代是 `flate/inflate_test.go:45` 的截断流 + `ErrUnexpectedEOF` + 已解前缀语义。
7. **zip fork 扩展 API**：`zip/writer_test.go:371`（Copy）、`zip/writer_test.go:424`（Copy）、`zip/writer_test.go:466`（CreateRaw）、`zip/writer_test.go:265`（SetOffset）。stdlib `archive/zip` 无 Copy/raw 写入；可移植 `zip/zip_test.go:22` 的 entry 数量/顺序、`zip/writer_test.go:300` 的 Flush 语义、`zip/zip_test.go:502` 的 zip64 边界（P2 4GB 虚拟目录）。
8. **gzhttp 包装层**：`gzhttp/compress_test.go:1020`、`gzhttp/transport_test.go:20` 依赖该库的 HTTP 中间件与 Transport。可用 `net/http` + `compress/gzip` 重写“Accept-Encoding 协商、最小长度阈值、随机 padding 边界、请求体解压”等价行为；不能复现其内部池与 zstd 编码分支。
9. **平台/构建标签**：`zstd/race_enabled_test.go:4`（`//go:build race`）、`zip/zip_test.go:503`（wasm 跳过）。属于构建标签/平台分支，可作为 L15 素材，但不属于可执行功能场景；本库测试中未发现 cgo。
10. **资源型超限用例**：`s2/encode_test.go:14`（MaxInt32/4GB block）、`flate/deflate_test.go:156`（2.3GB 稀疏流）、`zip/zip_test.go:502`（4GB 目录）、`zstd/zstd_test.go:66`（10MB×100）。并非不可移植，但必须标 P2 并支持缩放；否则在 CI 上易 OOM/超时，掩盖编译器问题。

## 结论

- 该库最适合产出 L05（Writer/Decoder/Scratch/池化复用、AllocsPerRun、MemStats）、L07（EncodeBuffer 所有权、cap 哨兵、零拷贝契约）、L11（多 level 确定性、matchLen、熵/位运算、零分配）与 L13（切片容量、截断流、多帧、65578 entry）用例。
- L08 只有“汇编 vs 纯 Go 结果一致性”和“overrun 模型”两类可借鉴素材；真实 SIMD/汇编覆盖需另取完整仓库或改由 sonic/fasthttp 等库承担。
- 压缩/解压往返校验统一用 stdlib `compress/gzip`、`compress/flate`、`compress/zlib` 与 `archive/zip`，不复制原库代码与私有格式。
