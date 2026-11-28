---
id: z03_reflect_makefunc_regabi
priority: P0
covers: [L17, L06, L11]
requires: [reflect]
default_seconds: 60
max_workers: 8
---

# 场景

`reflect.MakeFunc` + register ABI RegArgs 假栈对象（go#75778）。MakeFunc 生成的函数经 `makeFuncStub` 进入 Go 回调：指针实参先落在寄存器 / RegArgs 假栈对象中；若 GC 在该帧内发生而 RegArgs 未被扫描，参数指针失效（`marked free object in span` / canary 错 / 崩溃）。

每种变体在回调内**第一件事**执行 `runtime.GC()`，随后逐参数校验：

- 类型与 canary：`tag`、`v == rnV(seed,id)`；
- 尾部 pad 槽（发现部分 clobber）；
- **槽位号 `src`**：调用方给第 i 个实参写入 `src=i`，回调断言 `arg.src==i`，可发现寄存器/栈参数错位；
- 回调返回参数派生对象（`link` 指向某个实参、`src` 记录另一个实参 id），调用方据此复核。

| 变体 | 类型 | 调用方式 | 覆盖 |
|---|---|---|---|
| fn8 typed | `func(*rn ×6, string, *rn) *rn` | typed 函数值直接调用 | 7 指针 + string 打满 amd64 整数寄存器；参数只在寄存器/调用帧 |
| fn8 call | 同上 | `Value.Call` 传 8 个 `reflect.Value` | reflect 调用路径本身 |
| fn12 | 12 个 `*rn` | typed 调用 | 超出 9 个整数寄存器，强制 spill 到栈参数区 |
| variadic | `func(*rn, ...*rn) *rn` | typed 变参调用 | 回调收到打包后的 `[]*rn` Value |
| bigret | `func(*rn ×4) bigRet` | `Value.Call` | 含指针字段的大 struct 多字返回值 |
| nested | fn8 回调内 `Value.Call` 另一个 4 指针 MakeFunc | 双层 | 双层 makeFuncStub 帧；内层回调再 GC |

调用侧的 4KB 级对象在实参位置现场分配且调用返回后不再持有（typed 路径），GC 落在 stub 帧时参数的唯一根就是寄存器/RegArgs。独立 GC churn goroutine 高频 `runtime.GC()` 与调用重叠；`GOMAXPROCS=1` 时 churn 每次 `Gosched()+Sleep(1ms)`。每个变体每轮一次。

## 对应历史案例

| 历史案例 | 本场景对应 |
|---|---|
| [go#75778](https://github.com/golang/go/issues/75778) reflect.MakeFunc register-ABI RegArgs 假栈对象未被扫描 | fn8 typed / fn12 / variadic / bigret / nested 全部变体 |
| go#16249 类 liveness（结果参数/寄存器窗口） | fn8 typed 的“参数只在寄存器”形态 |

## 不变量

1. 回调内 GC 后 8/12 个参数指针全部有效，canary、尾部 pad、槽位号 `src` 正确（无错位、无回收）；
2. 回调返回的派生对象：`link` 指向的实参内容正确、`src` 与规定实参 id 一致；
3. bigret 返回的所有指针字段（`a/b/c[3]/e`）在返回后有效，标量字段 `d==4242`；
4. nested：内层 MakeFunc 经 `Value.Call` 的返回对象及其 link 在双层 stub 帧后仍有效；
5. 每轮每变体恰好调用一次；固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮，`started==finished`；
6. goroutine 回基线（+6 容差）；末行 `STRESS PASS z03_reflect_makefunc_regabi`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 2 | worker 数（≤8） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 种子，失败行带出 |
| `STRESS_FIXED_ROUNDS` | 8 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `phase=fn8|fn12|variadic|bigret/nested... argument corrupted after GC`：回调内 GC 后参数指针内容损坏——RegArgs/寄存器映射漏扫；
- `argument slot mismatch src=...`：参数寄存器/栈槽错位；
- `nil/typed-mismatch argument after GC`：参数指针被回收/清零；
- `tail pad corrupted`：对象被部分复用或越界写；
- `out: derived link object lost/corrupted`：返回路径丢失/破坏指针；
- `bigret: field ... corrupted`：多字返回值路径指针丢失；
- `nested...`：双层 stub 帧内参数/返回值损坏；
- `variant=... calls=... want=...`：worker 提前退出或轮次不足；
- `goroutines baseline ... leak`：辅助 goroutine 泄漏；`no progress`：watchdog 超时。

## 运行

```bash
SUITE=stress FILTER=z03 STRESS_SECONDS=3 PROFILES=soak,window REPEAT=1 TIMEOUT=120 ./run.sh
SUITE=stress FILTER=z03 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=z03 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 PROFILES=base ./run.sh
```

## 来源

- `docs/research/stress_history_bugs.md` #12 与 §2“register-ABI + reflect 调用路径”缺口；
- `docs/research/stress_batch5_design.md` z03；探针升级 §5“RegArgs 校验”；
- `reflect/makefunc.go`、`runtime/asm_amd64.s:makeFuncStub`、`internal/abi/regargs`（register ABI 假栈对象）。
