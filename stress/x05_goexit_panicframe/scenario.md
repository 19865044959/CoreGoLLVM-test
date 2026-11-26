---
id: x05_goexit_panicframe
priority: P0
covers: [L17, L18]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

Goexit / panic 帧与 “dead stack object” 形态（go#82394 / go#82917 风格）。u08 只校验展开帧内容，不测 “panic 记录保活死栈对象”；本场景补齐：

- **大 struct 按值传参**（约 264B，内含 16 个指针）给 `//go:noinline` 函数，callee 内显式 `runtime.GC()`，GC 前后校验全部指针；
- **闭包取址局部指针数组**（address-taken 栈对象/死栈对象形态），随后丢弃本地强引用 → `runtime.GC()` → `runtime.Goexit()`：
  - `exit_retain`：保留一个从旧帧带出的指针，Goexit 的 defer 链里再 GC 后校验 canary + weak 探针；
  - `exit_dead`：全部强引用丢弃后才 Goexit，defer 链里只做 GC 与求和（“确认无崩溃”形态）；
- **panic(value payload)**：大值 payload（>128B，含 8 指针 + 常量 words）panic，在外层 defer recover 后**先 GC 再校验**——展开期间内层帧已死，`_panic` 记录必须保活 payload；
- **panic(pointer payload) + weak.Make**：recover 后 GC，`weak.Value()` 必须仍指向被 panic 记录保活的对象；
- **三层 panic**：`panic → defer 内 runtime.Callers 走查栈 → runtime.GC → recover → 再 panic`，层层 big 按值传递；最上层 recover，逐层校验 payload；
- **panic + defer + WaitGroup + Gosched 交错**：多个 goroutine 同时展开，父 goroutine 反复 `Gosched` 并等待。

差异说明：真实 `runtime.Goexit` 可直接调用，本场景使用之；另两种展开语义由 `panic(value/pointer payload)+recover` 覆盖。不在 harness 内直接读取已回收的栈内存（未定义行为，stock 也可能崩）；“旧帧区域”判据一律走合法引用（defer 捕获指针 / recover 的 payload / weak 探针）或“无崩溃”。`STRESS_CRASH_FILE` 捕获 runtime 崩溃文本。

来源：go#82394、go#82917、`go/src/runtime/panic.go`（_panic 记录/Goexit）、`testprog/gcpanicframe.go`、`testprog/panicrace.go`、`crash_test.go`、`stack_test.go`；调研文档 x05。

## 不变量

1. 大 struct by-value 与局部数组指针在 callee 内 GC 前后 canary 全部一致；
2. Goexit 的 defer 链 GC 后，`exit_retain` 带出的指针仍有效、`weak.Value()!=nil`；`exit_dead` 不崩溃且校验和一致；
3. panic value/pointer payload 在展开 + GC 后 `n/slots/words` 完整；pointer 变体 weak 探针仍可见对象；
4. 每轮 panic 链恰好 3 层 recover；每层 defer 的 `runtime.Callers` 返回 ≥2 个非空帧；
5. panic+WaitGroup 变体每个子 goroutine 恰好 1 次 recover，指针有效；
6. `spawned == finished == rounds × STRESS_EXITS`；`sentinel_paths == rounds×2`；`panicWG == rounds×4`；
7. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；worker `started==finished`；
8. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
9. 最后一行 `STRESS PASS x05_goexit_panicframe`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | seed，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 8 | `seconds=0` 时每 worker 轮数 |
| `STRESS_DEPTH` | 192 | panic 链底层递归深度（16–2048） |
| `STRESS_EXITS` | 2 | 每轮 Goexit goroutine 数（1–16，retain/dead 交替） |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `canary ... (UAF/帧扫描漏标)`：panic 记录/死栈对象被提前回收或栈映射错误——最敏感于 `clobberfree`/`window`；
- `payload 类型=...`：panic/recover 值语义损坏；
- `payload 被改写` / `sum ... want ...`：大值 payload 跨展开/GC 被覆盖；
- `weak.Value()=nil`：panic 记录未保活对象，或 Goexit defer 路径漏扫描；
- `Callers 返回 0 帧` / `空函数名`：展开路径帧信息损坏（pcdata/帧指针问题）；
- `chain recoveries=... want=3`：再 panic 链断裂；
- `exit goroutines spawned != finished` / `6 个心跳周期无进展`：Goexit 展开卡死，附全栈；
- `goroutines baseline ... leak`：收尾泄漏；
- 只在 `window` 失败：漏标/并发窗口；`detector` 崩溃：释放对象被再次访问（UAF）。

## 运行

```bash
SUITE=stress FILTER=x05 STRESS_SECONDS=3 PROFILES=soak,window REPEAT=1 TIMEOUT=90 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=x05 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree REPEAT=1 TIMEOUT=120 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=x05 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 BUILD_FLAGS=-race PROFILES=base REPEAT=1 TIMEOUT=300 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
```

## 来源

- go#82394、go#82917（panic/Goexit 帧位置与 dead stack object）；
- `go/src/runtime/panic.go`（`_panic` 记录、Goexit 展开）、`testprog/gcpanicframe.go`、`testprog/panicrace.go`、`crash_test.go`、`stack_test.go`；
- `docs/research/stress_extreme_survey.md` x05 条目；R §2（A4/A8/A11）与 §3。
