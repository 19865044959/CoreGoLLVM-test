# stress 压测层调研结论（怎么写 / 能复用多少）

> 结论先行：**压测代码量不会很大**（手写约 2k 行 + 一个代码生成器）；真正的工作量在"负载形态 + 不变量 + 长跑编排"，不是堆代码。
> 复用优先级：**官方 stress 模式 > 测试语料 > 真实库源码**。库源码只建议选择性 vendor 一个（klauspost/compress）。

## 1. 代码量参考

| 来源 | 规模 | 说明 |
|---|---:|---|
| Go 官方 `test/stress/runstress.go` | 168 行 | 无限混合负载：map/channel 环/HTTP/exec/parse，`log.Fatalf` 即失败 |
| Go runtime 单测（malloc/stack/gc/crash） | 0.9–1.25k 行/个 | 短用例 + 外部超时；可提炼 GC/栈/子进程模式 |
| 本方案单个 stress 场景 | 250–450 行 | 阶段、不变量、心跳、参数化 |
| 6 个场景手写合计 | ~2k 行 | 不含生成的"大代码" |
| `s05` 生成代码 | 5k–10k 行（脚本生成） | 压编译器的结构/方法集/switch，不手写 |

所以不需要为"复杂"而写几万行；需要的是**多样化的运行时形态**和**长时间**。

## 2. 怎么写：从官方压测提炼的模式

详细依据见 `docs/research/stress_go_patterns.md`，关键几条：

1. **混合负载 + 子开关**：每个 flag 一个 forever goroutine，最后 `select{}`；失败 `log.Fatalf`。我们改成有时长/停止/`STRESS PASS`。
2. **独立 GC churn/分配器 goroutine**：业务 worker 与 `runtime.GC()` 循环分离；GC 次数只记录。
3. **栈增长 + GC 交织**：深递归每层放指针槽，返回途中逐层校验；`//go:noinline`、`debug.SetMaxStack` 限深。
4. **抢占风暴**：`GOMAXPROCS=1` + 空死循环/紧循环 + 并发 GC（官方 `testprog/preempt.go` 模式）；`asyncpreemptoff` 只作对照，不能配空循环等 GC。
5. **有界轮询代替 sleep 判定**：等事件/计数，带 slack；`NumGoroutine` 多次稳定采样，不用一次采样判泄漏。
6. **子进程隔离崩溃**：runner 每场景/profile/seed 起独立进程，白名单 env；`GOTRACEBACK=crash` / `debug.SetCrashOutput` 收集崩溃文本/core。
7. **不变量**：消息序号/校验和、环形保留对象内容、fresh 分配全零、started==finished、map key 集完整。
8. **watchdog**：每 worker 原子心跳，3 个周期无增长 → 打印阶段/seed + goroutine dump 后退出 3；外层 `timeout -s QUIT` 兜底。
9. **GODEBUG 对照**：`clobberfree` 必开；`efence` 只短跑；`gcshrinkstackoff` / `asyncpreemptoff` / `gcstoptheworld` 作对照组；所有"开关下才复现"都要 base 对照。

## 3. 能复用当前三方代码吗？分三类

### 3.1 测试语料：可以，选择性复用

手头 `research/repos/` 共 7434 个 testdata 文件 / 61MB（详见 `docs/research/stress_corpus.md`）：

| 语料 | 体量 | 适用 | 复用方式 |
|---|---|---|---|
| klauspost 压缩/fuzz 语料 | 41MB | s02 堆/压缩往返 | 不入库；运行时 `STRESS_CORPUS_DIR` 白名单读取；小样本（gzip/testdata 216KB）可入库 |
| sonic/goccy JSON | 1.6MB | s01/s05 | 小样本入库；推文类样本合成替代，避免隐私 |
| etcd raft 轨迹 | 91KB | s06 流水线/状态机 | 可入库（Apache-2.0），作期望状态参照 |
| hugo 图片、Go TLS/trace | 4MB/2.7MB | s01/s02 | 只取小样本；大件运行时读 |
| golangci-lint | 3MB | — | **GPL-3.0，禁用** |
| badger/nats/webrtc | 0 | — | 无 testdata，只能从测试代码提炼帧格式再合成 |

默认策略：**小样本（≤2MiB/场景）入库 + 大语料运行时目录 + 固定 seed 合成兜底**。

### 3.2 测试代码/场景：借鉴，不拷贝

三方库的 `*_test.go` 与库强绑定（私有字段、真实服务、testify 等），直接跑属于"库级差分"（已有其他人做）。stress 层只提炼**场景形态**（`docs/research/` 已有 28 份场景表），重写成 stdlib 压测程序。

### 3.3 真实库源码：只建议有选择地 vendor 一个

详见 `docs/research/stress_libraries.md`：

| 库 | 结论 | 理由 |
|---|---|---|
| **klauspost/compress v1.17.11** | **建议 vendor（可选场景 s07）** | 零依赖；`-tags=noasm,purego` 绕开 11 个 `.s`；gzip+flate+zlib 仅 356KB/28 文件；长跑压缩/池化/截断流价值高 |
| goccy/go-json v0.10.5 | 条件 vendor（需内网 canary） | 1.2MB/82 文件，零依赖，但 47 处 `go:linkname` 依赖 runtime/reflect 内部符号；锁 v0.10.5，勿升级 |
| sonic | 不引入源码 | JIT/asm/runtime linkname 与 ABI 强耦合；只取其 testdata |
| badger | 暂缓 | 纯 Go 但依赖闭包 11 模块/12MB vendor，磁盘 I/O 噪声大 |

第三方场景单独 `go.mod`+`vendor`，标 `requires: [thirdparty]`，默认不跑；`goc` 工具链是否识别 vendor 未验证，先只支持 `go/stock`。

## 4. 场景与复用映射（6 个标准 + 1 个可选）

| 场景 | 手写量 | 语料/模式 | 是否含三方 |
|---|---:|---|---|
| s01_service_soak | ~350 行 | JSON 小样本/合成报文；runstress 服务模式 | 否（可选 goccy） |
| s02_heap_cycle_soak | ~300 行 | klauspost 语料 + 合成 buffer；FreeOSMemory 循环 | 否 |
| s03_stack_churn_soak | ~250 行 | Go stack_test 模式 | 否 |
| s04_preempt_storm_soak | ~250 行 | Go preempt_test 模式 | 否 |
| s05_generated_code_soak | ~300 行 + 生成器 | protobuf/sqlc 形态，脚本生成 5k–10k 行 | 否 |
| s06_queue_pipeline_soak | ~350 行 | etcd raft 轨迹/合成事件流 | 否 |
| s07_compress_soak（可选） | ~250 行 | klauspost 真实库（vendor, noasm,purego） | 是 |

## 5. S0 落地计划（建议先做）

1. 建 `stress/` 骨架 + `scenario.md` 模板。
2. runner 加 `SUITE=stress`、`soak/soak-single/soak-noasync` profile、心跳停滞检测、`GOTRACEBACK=crash`。
3. 先写 **s02 + s03** 两个试点（不依赖三方库/语料）。
4. 官方 Go 1.24.1 上 60s × 3 profile 全绿、`-race` 一次通过后，再补 s01/s04/s05/s06。
5. 语料与 s07 vendor 在 s02/s03 跑通后再决定；默认 CI 不启用 s07。

## 6. 已启动

- `stress/` 骨架与 `SUITE=stress` runner 已落地；
- S0 试点：`s02_heap_cycle_soak`（堆循环）、`s03_stack_churn_soak`（栈 churn）；
- 验证：5s × 3 soak profile + `-race` 通过；60s × 3 soak profile 全绿。
- 后续按 §5 顺序补 s01/s04/s05/s06，再评估语料与 s07。
