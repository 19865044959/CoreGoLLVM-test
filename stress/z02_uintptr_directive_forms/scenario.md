---
id: z02_uintptr_directive_forms
priority: P0
covers: [L17, L18]
requires: []
default_seconds: 60
max_workers: 8
---

# 场景

`//go:uintptrescapes` 的 5 种调用形态（go#34474 / go#34684）。指令要求：被调函数的 `uintptr` 实参若由 `unsafe.Pointer` 转换而来，调用方必须把源对象保活到调用返回。编译器只在“静态已知被调函数”的调用点注入该 keepalive；任一形态漏注入，对象在调用内的 GC 中即被回收。

| # | 形态 | 调用写法 | 官方 Go 1.24.1 实测 | 本场景判定 |
|---|---|---|---|---|
| a | direct | `consumeDirect(pr, f, r, uintptr(up))` | keepalive 生效 | **strict**：finalizer/weak + 内容 |
| b | method_value | `m := vt.consume; m(pr, f, r, uintptr(up))` | 调用内即被回收 | content-only（强引用跨调用 + 只校验内容） |
| c | method_expr | `(*upTarget).consume(vt, pr, f, r, uintptr(up))` | keepalive 生效（静态直接调用） | **strict** |
| d | func_value | 经 `func` 变量/参数调用，被调函数不可见 | 调用内即被回收 | content-only |
| e | iface_method | 经接口方法动态分派 | 调用内即被回收 | content-only |

- 被调函数体内第一件事是 `runtime.GC()`（执行两次完整周期，确保不可达对象在 weak 表中已清除），随后把 `uintptr` 转回对象校验 `id/v/tag` canary。
- 对象为 **4KB（`upBig`）与 64KB（`upHuge`）大对象**，避开 tiny/小对象分配器与大对象 span 的复用干扰；每个对象注册 finalizer（调用期间被终结 = FAIL）+ `weak.Make`（调用期间 `Value()==nil` = FAIL）。
- 每个形态每轮各跑一个 4KB + 一个 64KB 对象，失败行带形态名与对象大小。
- 形态 b/d/e 在官方 Go 上不保证指令生效（go#34684 开放风险面；接口方法上写该指令会被 `go1.24.1` 直接拒绝：`misplaced compiler directive`），故降级为“用 `runtime.KeepAlive(up)` 把对象强持有到调用返回 + 只校验 uintptr 往返内容”，避免在合法程序上产生误报；a/c 保持严格 finalizer/weak 断言，若编译器回归即可当场判失败。
- 独立 GC churn goroutine 并发施压；`GOMAXPROCS=1` 时 churn 每次 `Gosched()+Sleep(1ms)`。

## 对应历史案例

| 历史案例 | 本场景对应 |
|---|---|
| [go#34474 / #34684](https://github.com/golang/go/issues/34684) `//go:uintptrescapes` 对方法调用/方法表达式/函数值/接口不生效 | 5 形态矩阵；a/c strict，b/d/e content-only |
| go#34684 中“指令只在直接调用形态注入 keepalive”的结论 | 见上表实测列 |

## 不变量

1. strict 形态（direct / method_expr）：调用内 GC 后 `weak.Value()!=nil`、finalizer 计数与 channel 通知均为 0，且对象内容（`id/v/tag`）正确；
2. content-only 形态（method_value / func_value / iface_method）：调用内 GC 后 `uintptr→对象` 往返的内容正确（强引用由调用方保活到调用返回）；
3. 4KB 与 64KB 两种大小在每形态每轮各执行一次；每形态调用数 = 轮数 × 2；
4. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮，`started==finished`；
5. 收尾“立即失活”探针的 finalizer 在失败上限内实际执行（探针有效性）；
6. goroutine 回基线（+6 容差）；末行 `STRESS PASS z02_uintptr_directive_forms`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 2 | worker 数（≤8） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 种子，失败行带出 |
| `STRESS_FIXED_ROUNDS` | 8 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

每轮 5 形态 × 2 大小 = 10 次调用（每次调用内 2 个 GC 周期）。

## 失败判读

- `in-call form=direct|method_expr ... weak ... Value()==nil`：strict 形态的对象在调用内被回收——`uintptrescapes` keepalive 未注入（编译/逃逸分析回归）；
- `in-call form=direct|method_expr ... finalizer ran during call` / `finalizer notification during call`：同上，且 finalizer 已实际执行；
- `in-call/content form=... corrupted`：`uintptr` 往返后对象内容损坏（对象被回收/复用，或 ABI 传参错误）；
- `form=... calls=... want=...`：worker 提前退出或轮次不足；
- `final: 已失活 probe 的 finalizer 未在 15s 内执行`：终结器机制失效/饥饿；
- `goroutines baseline ... leak`：辅助 goroutine 泄漏；`no progress`：watchdog 超时。

## 运行

```bash
SUITE=stress FILTER=z02 STRESS_SECONDS=3 PROFILES=soak,window REPEAT=1 TIMEOUT=120 ./run.sh
SUITE=stress FILTER=z02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=z02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 PROFILES=base ./run.sh
```

## 来源

- `docs/research/stress_history_bugs.md` #15 与 §2“编译器指令遵守”缺口；
- `docs/research/stress_batch5_design.md` z02；
- `cmd/compile/doc.go` `//go:uintptrescapes` 语义；`internal/escape/call.go:rewriteArgument`（只在静态已知被调函数时注入 keepalive）；
- `test/fixedbugs/issue34474.go` / go#34684 讨论。
