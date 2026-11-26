---
id: x06_coro_iterpull
priority: P0
covers: [L01, L16, L17]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

`iter.Pull` 协程（coro）切换 × GC 双栈扫描。现有 30 个场景完全无 coro；CorGoLLVM 的 `//go:linkname` 新指令与 `coroswitch` m 栈切换路径此前未覆盖。

每 worker 每轮五个阶段：

1. **GC 窗口段**（`gcPulls=24`）：生成器逐次产出带指针 payload（`*node` + interface + `[]*node`，canary）；消费者**每次 pull 之间显式 `runtime.GC()`**——此时 consumer 栈（上一 payload、anchor）与挂起的 coro 栈（生成器局部 `nd/held/sib`）上都有活跃指针；每次 GC 后校验上一 payload 与 `weak.Make(anchor)` 存活；
2. **高速切换段**（`fastPulls=16384`）：不逐次 GC，并行 GC churn 仍持续触发；校验 canary 与闭式 checksum；
3. **嵌套 Pull 段**（外层 48 × 内层 64）：生成器内部再 `iter.Pull` 另一个生成器（coro 套 coro）；每 8 次 pull 一次 GC，校验内外层指针；
4. **panic 传播段**：生成器内 panic → 消费者 `next()` 处 re-panic → `recover` → GC 后校验 payload 指针；panic 后 `next` 恒为 `zero,false`，`stop` 幂等；
5. **LockOSThread 段**：消费者先 `LockOSThread` 再创建 Pull（coro 继承线程锁状态，`coroswitch` 校验 m/lock 一致），pull 8 次后提前 `stop`，校验 `stopped==1`、`next` 恒 false。

`stop` 后泄漏检查 = 每段结束后 `next` 恒 `zero,false` + 收尾 goroutine 回基线。仅标准库；`iter.Pull` 需 go1.23+（官方 1.24 基线可用）。不做墙钟吞吐断言，切换总量由固定轮数与每段固定 pull 数控制。

来源：`go/src/iter/iter.go`（Pull/panicValue/goexitPanicValue）、`go/src/runtime/coro.go`、`iter/coro_test.go`；调研文档 x06。

## 不变量

1. 每个 payload 的 `id/v/n/box/list` 与确定性公式一致；coro 恢复后生成器局部 `nd/held/sib` canary 不变；
2. GC 窗口段每次 GC 后：上一 payload 全部有效、`weak.Value() == anchor`（strong-reachable）；
3. 高速段 checksum == `Σ nodeMagic(seed,id)`，pull 数精确等于 `fastPulls`；
4. 嵌套段 pull 数 == `nestedOuter`，内外层指针在 GC 后有效；
5. panic 传播段恰好 1 次消费者侧 recover、payload 类型/指针正确；panic 后与 stop 后 `next` 均返回 `zero,false`；`stop` 可重复调用；
6. 每轮 3 个生成器正常完成（GC 段/高速段/嵌套外层）、1 个被提前 stop（LockOSThread 段）；
7. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；worker `started==finished`；
8. 收尾 quiesce 后 goroutine 回基线（+4 容差，coro goroutine 不得泄漏）；
9. 最后一行 `STRESS PASS x06_coro_iterpull`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | seed，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 8 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

各段 pull 数量为包内常量（24/16384/48×64/8），保证固定轮数下总量确定、可重放。

## 失败判读

- `canary ... (coro 栈扫描漏标/UAF)`：coroswitch 挂起栈未被正确扫描，或 payload 被提前回收；
- `held 指针被改写`：生成器局部在切换后损坏；
- `checksum ... want ...`：高速段值/顺序错乱；
- `next 未传播 panic` / `panic payload 类型=...`：iter.Pull panicValue 路径损坏；
- `stop 后 next 仍返回` / `gen stopped ... want=1`：stop 语义或 coro 退出失败；
- `gen completed ...`：生成器未按预期完成（提前结束/重复完成）；
- `coro: OS thread locking must match locking at coroutine creation`（runtime throw 文本）：LockOSThread 变体的 m/lock 校验失败，crash 文本见 `STRESS_CRASH_FILE`；
- `goroutines baseline ... leak`：coro goroutine 未退出（stop/panic/嵌套停止路径泄漏）；
- 只在 `window`/`soak-single`（GOMAXPROCS=1）失败：单 P 下 coro 切换/锁状态问题。

## 运行

```bash
SUITE=stress FILTER=x06 STRESS_SECONDS=3 PROFILES=soak,window,soak-single REPEAT=1 TIMEOUT=90 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=x06 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree REPEAT=1 TIMEOUT=120 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=x06 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 BUILD_FLAGS=-race PROFILES=base REPEAT=1 TIMEOUT=300 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
```

## 来源

- `go/src/iter/iter.go`（Pull/next/stop、panicValue、goexitPanicValue）；
- `go/src/runtime/coro.go`（newcoro/coroswitch/coroexit、线程锁状态校验）、`iter/coro_test.go`；
- `docs/research/stress_extreme_survey.md` x06 条目；R §2（coro/`//go:linkname` 适配风险）与 §3。
