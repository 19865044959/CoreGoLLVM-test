# 维度定义与 unit 用例规范

本仓库（CoreGoVLLM-test）分两层：`cases/`（unit 语义回归，本文件）与 `stress/`（长跑压测，见 `docs/STRESS_PLAN.md`）。

unit 层按 **19 个 Go 语言特性维度**组织用例：原始 L1–L17 来自《Go 编译器稳定性测试三方库清单（Go 1.24.1）》，另增 L18/L19（见下）。目标是每个维度 ≥50 个运行级用例。

## 一、19 个维度

| 维度目录 | 名称 | 主要机制 | 重点库 | 阶段 |
|---|---|---|---|---|
| L01_generics | 泛型 | 类型参数/约束/实例化/泛型方法/泛型别名 | pgx、go-redis、quic-go、ent、kitex、traefik、gitea、yaegi、sqlc、golangci-lint | 3 |
| L02_concurrency | 高并发 | goroutine/channel/select/sync/atomic/context | caddy、frp、gin、grpc-go、quic-go、nats-server、consul、tailscale | 2 |
| L03_async_preempt | 异步抢占 | 安全点、长循环、SIGURG、`asyncpreemptoff` | kubernetes、prometheus、sonic、grpc-go、nats-server、goja、wazero | 2 |
| L04_scheduler_stack | 协程调度/栈 | GPM、netpoll、work stealing、morestack、栈复制/收缩 | 高并发服务类库 | 2 |
| L05_alloc_gc | 大量内存分配 | 堆分配、逃逸、GC、写屏障、sync.Pool、大对象、切片/字符串拼接 | 所有库（重点 sonic、gorm、pgx、badger、goja、wazero） | 1 |
| L06_reflect_iface | 反射与接口 | reflect、interface、断言、type switch、itab、方法集 | gorm、echo、gitea、pgx、protobuf-go、delve | 2 |
| L07_unsafe_zerocopy | unsafe 与零拷贝 | unsafe.Pointer、uintptr、string↔[]byte、`//go:noescape`、内存布局 | fasthttp、sonic、goccy/go-json、badger、protobuf-go、go-ethereum、wazero | 2 |
| L08_asm_simd | 汇编与 SIMD | 手写 .s、ABI0/ABIInternal、寄存器分配、SIMD | klauspost/compress、sonic、fasthttp、go-ethereum、wazero、minio | 4 |
| L09_cgo | cgo | import "C"、C 调用约定、CGO_ENABLED、指针传递 | go-ethereum、containerd、tailscale、minio | 4 |
| L10_codegen | 代码生成 | protobuf/Thrift、go:generate、模板/AST 生成代码 | grpc-go、kitex、ent、protobuf-go、sqlc、consul、restic | 3 |
| L11_compiler_opts | 编译期优化 | 内联、逃逸分析、SSA、常量传播、DCE、边界检查消除 | gin、hugo、klauspost/compress、fasthttp、echo、goccy/go-json、gorm、goja、golangci-lint | 3 |
| L12_init_link | 初始化与链接 | init 顺序、包级变量初始化、插件、符号可见性、DUPOK | caddy、terraform、kubernetes、prometheus、fiber、kitex、traefik、consul | 3 |
| L13_runtime_ds | 运行时数据结构 | map/slice/string、扩容、哈希、Swiss map | etcd、tidb、sonic、goccy/go-json、pgx、badger、go-ethereum | 1 |
| L14_debug_info | 调试信息 | DWARF、行号表、变量位置、内联栈、栈展开 | delve | 4 |
| L15_build_tags | 构建标签与多平台 | //go:build、GOOS/GOARCH、文件后缀、平台特化 | quic-go、pion/webrtc、otel-go、containerd、traefik、consul、tailscale | 4 |
| L16_template_interp | 模板/解释器/元编程 | text/html/template、go/ast、go/types、字节码/解释执行 | hugo、goja、yaegi、gitea、wazero、golangci-lint、sqlc | 3 |
| L17_stackmap | 栈映射 | 安全点、FUNCDATA/PCDATA、gcargs/gclocals、栈扫描/复制/抢占/展开 | 所有库（重点 sonic、fasthttp、go-ethereum、wazero、goja、nats-server、delve） | 1 |
| L18_gc_lifecycle_wb | GC 生命周期与写屏障（新增） | 老→新引用、写屏障、finalizer、sync.Pool、对象存活/复活、堆零化 | badger、nats-server、goja、prometheus | 1 |
| L19_netpoll_timer_signal | netpoll/定时器/信号（新增） | netpoll、timer heap、SIGURG、阻塞 syscall、栈切换、graceful shutdown | nats-server、grpc-go、quic-go、caddy、traefik、containerd | 2 |

> L18 与 L17 的区别：L17 验证"栈/寄存器里的指针位图是否正确"，L18 验证"堆对象之间的引用在 GC 周期中是否被正确保留/清理"（写屏障、finalizer、池化复用、零化）。
>
> L19 覆盖现网（k8s 服务）最真实的 GC 触发源：定时器、netpoll、信号抢占、阻塞系统调用。

## 二、目录与命名

```
cases/<维度目录>/<用例目录>/main.go
cases/<维度目录>/<用例目录>/case.md
```

- 用例目录名 = 测试名 = `ALL PASS` 后面的名字，全局唯一，建议 `l05_<场景>_<变体>`。
- 一个用例可以覆盖多个维度：`case.md` 的 `dim` 写主维度，`tags` 写交叉维度/机制。
- 旧用例已删除重建，不再保留历史命名。

## 三、case.md 契约

```markdown
---
id: l05_pool_churn_slice
dim: L05_alloc_gc
priority: P1
sources:
  - sonic@v1.13.2: decode_test.go 的 pool/reuse 场景
tags: [sync.Pool, slice-growth, write-barrier]
requires: []
runtime: "<10s"
---
```

- `priority`：`P0`（<2s，每次编译器改动必跑）、`P1`（<10s，每日）、`P2`（长压测，分钟级）。
- `requires`：能力标签（如 `generics`、`templates`、`reflect`、`cgo`、`asm`、`dwarf`）。内网用 `CAPS=generics,reflect` 只跑可支持用例；不在 CAPS 里的用例会被 runner 跳过，避免假失败。
- `sources`：来源库/测试文件/场景。必须能追溯，不逐行拷贝，只借鉴场景。

正文必须包含：场景、期望行为（逐条）、失败判读、参数、运行命令。

## 四、用例硬性要求

1. 单包 `package main`，不依赖三方库（CoreGoLLVM 内网可独立编译）。
2. 输出确定：不打印地址、时间、随机数、map 遍历顺序；最后一行 `ALL PASS <用例目录名>`。
3. 自校验：失败打印 `FAIL: ...` 并最终退出码 1；崩溃由 runner 按退出码捕获。
4. GC 必须落在关键位置：用 `runtime.GC()` + 垃圾分配，不能只靠 `GOGC`。
5. 支持 `UAF_SEED`（有随机路径时），失败输出带种子。
6. 运行时间默认 <10s；长压测用例标 `P2` 并支持 `TORTURE_*` 参数放大。
7. 可选文件：`.skip-envs`（跳过的 profile）、`.env`（额外环境变量）、`.expected`（golden 输出，含 stderr）。

## 五、验收标准（每个维度）

- ≥50 个用例，其中至少 10 个 P0/P1 能在 60s 内跑完；
- 100% 有 `case.md`，`tools/gen_matrix.py` 能生成矩阵；
- 在官方 Go 1.24.1 上全 profile 通过（`ORACLE=1` 时输出一致）；
- 在 CoreGoLLVM 上失败时，能凭 `case.md` 的失败判读区分"编译器 bug"与"用例/环境问题"。
