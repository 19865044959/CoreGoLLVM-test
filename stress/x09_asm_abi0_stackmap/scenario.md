---
id: x09_asm_abi0_stackmap
priority: P0
covers: [L08, L17, L05]
requires: [asm, amd64]
default_seconds: 60
max_workers: 32
---

# 场景

ABI0 汇编帧 args/results stackmap 长跑（linux/amd64；`BUILD_MODE=module` + `asm_amd64.s`）。

手写汇编函数全部为 `NOSPLIT` + 严格 ABI0，Go 侧只有声明，参数/结果全部落在 FP 槽：

| 函数 | ABI0 布局 | frame/argsize | 测什么 |
|---|---|---|---|
| `asmPtrCall(p *node, x int64) (*node, int64)` | `p+0(FP) x+8(FP) ret+16(FP) canary+24(FP)` | `$8-32` | `*node` 入参活跨 asm 内 `CALL ·helperCanary`（helper 内 GC + 批量分配）；返回指针原样透传；GC 后重读 `p.v` 并与 helper 返回值合并校验 |
| `asmPtrReturn(p *node, x int64) (*node, int64)` | `p+0(FP) x+8(FP) ret+16(FP) chk+24(FP)` | `$8-32` | helper 返回 `*node` 写入 result 槽后 `GO_RESULTS_INITIALIZED`，再 `CALL ·helperSink`（GC + 批量分配）；结果指针只由汇编帧 results bitmap 保活；入参 p 也活跨该 GC |
| `asmLoop(p *node, n int64) int64` | `p+0(FP) n+8(FP) ret+16(FP)` | `$24-24` | 计数循环内每轮 `CALL ·helperCanary`，p 活跨全部调用；计数/累加存于无指针局部槽 |
| `asmChain0..4(p *node, n int64) int64` | `p+0(FP) n+8(FP) ret+16(FP)` | `$32-24`（chain4 `$0-24`） | 有界汇编递归链，每层调 Go helper 并透传其返回值。NOSPLIT 直接自递归会被链接器判为 `infinite cycle`，故展开为 5 个互不相同的 NOSPLIT 符号 |

关键语义（严格 ABI0）：

- 每个汇编函数都写 `GO_ARGS`（args pointer map）与 `NO_LOCAL_POINTERS`；`asmPtrReturn` 在写入 result 后写 `GO_RESULTS_INITIALIZED`，使 results bitmap（inputs ∪ results）生效；
- Go ABI 没有 callee-saved 寄存器：每次 `CALL` 返回后都从 FP 重新载入指针、从局部槽读回计数/累加；
- 指针参数只由汇编帧的 args/results map 保活：driver 分配 p 并调用汇编后不再使用 p（`drivePtrCall/drivePtrReturn/driveLoop/driveChain` 均 noinline），而 helper 的 GC 后立即分配同 size class 对象，迫使任何被误回收的槽被复用/`clobberfree` 覆盖；
- 对照的纯 Go 版本 `goChainValue` 复用同一 helper 调用序列，asm 结果必须与之一致。

`asmPtrReturn` 的两项检查分别验证 result 指针与 args 指针；`asmPtrCall` 的返回 canary = helper 常数 + GC 后重读的 `p.v`。`runtime.GC()` 在 helper 内执行，`runtime.NumGC` 只记录。

## 不变量

1. `asmPtrCall` 返回的指针等于入参对象：GC 后 `v/name` 正确，返回 canary = helper 常数 + 原 `p.v`；
2. `asmPtrReturn` 的 helper 结果指针在 `helperSink` 的 GC 后仍指向 `{v=0x9E3779B9,name="x09alloc"}`，且入参 p 的 `v` 未被改写；
3. `asmLoop` 结果 = `p.v + loopN*helperCanary`；
4. `asmChain0..3` 每层 helper 返回值透传正确，且 `asm == go` 对照值一致；
5. 调用者再次 `runtime.GC()` 后，返回的 arg/result 指针内容仍正确；
6. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；心跳持续增长、watchdog 无停滞；
7. 收尾 quiesce 后 goroutine 数不超过启动基线 + 4；
8. 最后一行 `STRESS PASS x09_asm_abi0_stackmap`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤32） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `STRESS_FIXED_ROUNDS` | 64 | `seconds=0` 时每 worker 轮数 |
| `UAF_SEED` | 1 | x 值种子，失败输出带出 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

场景常量：helper 每批 64 个 node（与 node 同 size class）、loopN=8、chainN=4。

## 失败判读

- `phase=asm-ptr-call ... arg pointer corrupted`：汇编帧 args map 未标记入参 p，GC 回收了仍被汇编帧引用的对象（或 clobberfree/复用覆盖）；
- `phase=asm-ptr-call ... helper result/pointer read corrupted`：GC 后重读 `p.v` 失败，或 helper 返回值透传损坏；
- `phase=asm-ptr-return ... result pointer corrupted`：result 槽未生效（缺 `GO_RESULTS_INITIALIZED`/results bitmap 错误），helperAlloc 返回的对象在 helperSink 的 GC 中被回收；
- `phase=asm-ptr-return ... arg pointer ... corrupted`：入参 p 活跨 helperSink 时未被 args map 标记；
- `phase=asm-loop ... checksum=`：p 活跨计数循环内多次 Go 调用时未被标记，或循环计数/局部槽被 clobber；
- `phase=asm-chain ... asm=... go=...`：递归链中指针透传/累加与 Go 对照不一致（栈映射或 ABI0 布局错误）；
- `fatal error: missing stackmap` / `frame ... untyped locals`：汇编函数缺少 `NO_LOCAL_POINTERS`（或局部槽放了未声明的指针）；
- 构建错误 `nosplit stack over ... infinite cycle`：把 NOSPLIT 汇编写成直接自递归；应使用有界符号链；
- 构建错误 `invalid offset`/帧尺寸不符：FP 偏移或 `$frame-argsize` 声明与 Go 声明不一致；
- 非 amd64 平台：`asm_amd64.s` 不参与构建（`//go:build amd64`），用 `CAPS=asm,amd64` 过滤；
- `-race` 构建：本场景在 stock Go 1.24.1 下 asm + race 可用；若目标工具链不支持，用 `PROFILES=base` 复跑并注明。

## 运行

```bash
SUITE=stress FILTER=x09 STRESS_SECONDS=60 PROFILES=soak ./run.sh
SUITE=stress FILTER=x09 STRESS_SECONDS=3 PROFILES=soak,window REPEAT=1 TIMEOUT=120 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=x09 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
SUITE=stress FILTER=x09 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 BUILD_FLAGS=-race PROFILES=base ./run.sh
# 手动构建：
cd stress/x09_asm_abi0_stackmap && go build -o /tmp/x09 . && GODEBUG=clobberfree=1 /tmp/x09
```

## 来源

- `docs/research/stress_extreme_survey.md` x09（asm 帧 × args stackmap × Go callee 内 GC）
- `docs/GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md` §A8 / §3.2 P0#19（GoAsmFuncStackMap）、go#74851
- `cases/L08_asm_simd/l08_asm_abi0_amd64`（ABI0 指针透传/字段读取基线）
- `go/src/runtime/funcdata.h`（GO_ARGS / NO_LOCAL_POINTERS / GO_RESULTS_INITIALIZED 语义）
- `go/src/cmd/compile/internal/liveness/plive.go`（`WriteFuncMap`：inputs 与 results 两张 bitmap）
- `go/src/runtime/stkframe.go`、`mgcmark.go`（汇编帧 args/results map 的扫描路径）
