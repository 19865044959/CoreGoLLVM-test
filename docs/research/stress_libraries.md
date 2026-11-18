# stress 嵌入真实三方库源码可行性调研

- 日期：2026-11-18；环境：Go 1.24.1 linux/amd64，`CGO_ENABLED=0`，官方 toolchain。
- 源码位置（module cache，未写入仓库）：`$(go env GOMODCACHE)/<module>@<version>`。
- 方法：独立 `/tmp` module + 最小 workload（goccy 编解码/流式、klauspost gzip+zstd 往返、sonic 编解码、badger 开库/1000 写读/关库），冷构建用独立 `GOCACHE`。

## 结论

- **值得引入**：`klauspost/compress`（首选，纯 Go 回退已验证、零依赖、加 0.4–1.3MB）；`goccy/go-json`（次选，压测价值高，但 47 处 linkname 需先在内网做编译 canary）。
- **不引入源码**：`sonic`（JIT/asm/runtime linkname，amd64+go1.24 必走 native，CoreGoLLVM 大概率不可用）；`badger`（能编能跑，但 11 个传递模块/12MB vendor，成本高于收益）。
- 两库合计 vendor 约 1.6–2.6MB、约 110–190 文件；远小于 `research/repos`（约 170MB），对仓库增重可忽略。

## 规模与依赖统计

| 库 | 版本 | 非测试 LOC/文件 | 测试 LOC/文件（测试:源码） | go.mod 直接/间接 | 构建闭包外部模块 | asm | cgo | linkname | 整模块体积 | 最小 vendor |
|---|---|---|---|---|---|---|---|---|---|---|
| goccy/go-json | v0.10.5 | 36,097 / 74 | 55,469 / 39（1.54:1） | 0 / 0 | 0 | 0 | 0 | 47 处 / 8 文件 | 2.6M | 1.2M / 82 文件 |
| klauspost/compress | v1.17.11 | 46,264 / 131 | 26,070 / 61（0.56:1） | 0 / 0 | 0 | 11 个 `.s` | 0 | 0 | 46M（testdata≈42M） | gzip+flate+zlib 356K/28；+zstd 1.3M/109 |
| bytedance/sonic | v1.13.2 | 146,162 / 317 | 29,890 / 135（0.20:1） | 7 / 2 | 5 | 33 个 `.s` + 41 个 C/H | 0 个 import "C" | 13 文件 | 18M | 16M / 607 文件 |
| dgraph-io/badger/v4 | v4.5.0 | 22,111 / 66 | 15,109 / 33（0.68:1） | 12 / 9 | 11 | 0 | 0 | 0 | 5.0M | 12M / 758 文件 |

注：vendor 大小用 `go mod vendor` 实测，按最小 workload 的导入闭包裁剪；不含 testdata。许可证：goccy MIT；klauspost BSD-3；sonic/badger Apache-2.0，vendor 时必须保留 LICENSE/NOTICE。

## 构建与运行实测（官方 Go 1.24.1）

| workload | 冷构建 | 热构建 | 二进制约 | 运行 |
|---|---|---|---|---|
| goccy：Marshal/Unmarshal + Decoder + RawMessage | 15.2s | 0.33s | 3.92MB | 正常 |
| klauspost：gzip 往返 + zstd EncodeAll/DecodeAll | 5.0s | 0.25s | 3.61MB | 正常 |
| klauspost：仅 gzip+zlib | 4.1s | — | 2.55MB | 正常 |
| sonic：Marshal/Unmarshal | 6.7s | 0.40s | 7.58MB | 正常（stock） |
| badger：Open + 1000 写 + 读 + Close | 13.0s | 0.56s | 14.33MB | 4.03s（磁盘 fsync 主导） |

冷构建含 stdlib 编译，仅用于横向比较。klauspost 另验证 `-tags=noasm`、`-tags=noasm,purego`、离线 `-mod=vendor` 均可编译运行；goccy/sonic/badger 的离线 vendor 构建也通过。

## 可行性矩阵（对 CoreGoLLVM）

| 库 | 编译风险 | 运行风险 | 压测收益 | 决策 |
|---|---|---|---|---|
| klauspost/compress | 低：加 `noasm,purego` 即绕开全部汇编；每包有纯 Go 回退 | 低：无 unsafe/linkname/cgo | 中高：L05/L07/L13/L19，长跑压缩流、池化 buffer、截断流 | **vendor，首选** |
| goccy/go-json | 中：47 处 linkname 指向 reflect/runtime 内部符号，无关闭 tag | 中：map/rtype 布局与 swissmap 兼容性 | 高：L05/L07/L13/L17/L18，JSON 分配风暴、RawMessage 别名生命周期 | **条件 vendor**：内网 BUILD_ONLY canary 通过后再启用 |
| bytedance/sonic | 高：linux/amd64+go1.17–1.24 强制 native JIT；compat 仅 go1.25/非 amd64，`-tags=go1.25` 强制切换会编译失败 | 高：可执行 mmap、moduledata/funcdata 改写、写屏障 linkname | 名义上高，实际不可达 | **跳过源码** |
| dgraph-io/badger/v4 | 中低：纯 Go、无 asm/cgo/linkname | 中：mmap/fsync、unsafe SliceHeader/arena；依赖面大 | 高：LSM/迭代器/值日志 UAF、L05/L13/L17/L18 | **暂缓** |

### 关键风险证据

- goccy：`internal/{decoder,encoder,runtime}` 共 47 条 `go:linkname`，含 `runtime.mapiterinit`、`reflect.mapiterelem`、`reflect.makemap`、`reflect.typedslicecopy`、`reflect.typedmemmove`、`reflect.(*rtype).*` 等；47 个非测试文件使用 unsafe。v0.10.5 无汇编，但 v0.12.0 已新增 amd64/arm64 `.s`，**必须锁版本、勿升级**。
- klauspost：11 个 `.s` 的约束为 `amd64 && !appengine && !noasm && gc`（arm64 另有 s2/xxhash），关闭 `gc` 或加 `noasm`/`purego` 后自动落到通用实现；已实测纯 Go 路径 roundtrip 一致。若 CoreGoLLVM 设置了 `gc` tool tag 却不支持汇编，必须显式加 tag，否则构建期直接报错（可检测，不会静默）。
- sonic：JIT 依赖 `golang-asm`/`x/arch` 生成机器码、`sonic/loader` 修改 funcdata、linkname `runtime.gcWriteBarrier`/`mallocgc`/`growslice`；这些都是与本仓 ABI/栈映射强耦合的路径，不适合作为被测负载。
- badger：mmap 经 `ristretto/z`，对象布局依赖 `unsafe.Pointer`/`SliceHeader`；依赖闭包含 protobuf、x/sys、x/net、opencensus，vendor 12MB/758 文件，且磁盘模式 I/O 噪声大（建议 InMemory 模式）。

## 建议

1. **第一批只 vendor `klauspost/compress v1.17.11`**：先取 `flate`+`gzip`+`zlib`（约 356KB/28 文件、约 9.4k LOC），需要 zstd 再加到 1.3MB/109 文件。场景形态：多 goroutine gzip/zstd 往返 + 校验和 + Writer/Reader 复用 + 截断流 + 周期性 GC，纳入 60s soak。构建使用 `-mod=vendor -tags=noasm,purego`；在 `scenario.md` 标 `requires: [thirdparty]`。
2. **第二批条件引入 `goccy/go-json v0.10.5`**（1.2MB/82 文件，零依赖）：先在 CoreGoLLVM 内网加一个 BUILD_ONLY + 10s 冒烟 canary；能过再加入 JSON soak（大 struct/map 编解码、RawMessage 子切片、流式多文档、轮间 GC）。过不了就退回“仅借鉴其测试形态”。
3. **sonic 只取 testdata**：`testdata/` 约 1.5MB（twitter/citm/canada/synthea，多为 `.json.gz`），解压或按 gzip.Reader 喂给 goccy/stdlib workload，避免复制其源码与 16MB vendor。
4. **badger 暂不 vendor**；若后续确需 LSM 形态，单独做 InMemory 场景并评估 12MB 依赖闭包。klauspost 的 42MB testdata 不整体复制，只按需摘取 `gzip/testdata`（216KB）等小样本。
5. **集成约束**：第三方场景使用独立 `go.mod`+`vendor`，跑法与现有 `BUILD_MODE=module` 兼容（`TOOLCHAIN=go/stock`）；`run.sh` 的 `BUILD_FLAGS` 是全局的，建议增加 per-scenario `.buildflags` 或 `requires`→tags 映射。`TOOLCHAIN=goc` 走 `llvm-goc ... .`，是否识别 vendor 未验证，第三方场景先只支持 `go/stock`。
6. **增重估算**：klauspost-only ≈ +0.4MB（含 zstd +1.3MB）；再加 goccy ≈ +2.5MB；两者合计 <200 文件，git 压缩后更小。对比：badger +12MB/758 文件，sonic +16MB/607 文件，均不建议。
