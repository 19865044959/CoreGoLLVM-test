---
id: x01_aggregate_statepoint
priority: P0
covers: [L17, L05]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

聚合 statepoint：对准报告 A4/A5——FCA（flattened composite aggregate）抽取只支持部分聚合形态，嵌套数组在 release 下可能静默 miscompile。每个 worker 每轮用确定性 canary payload 构造 14 种聚合 shape，全部经 `//go:noinline` 边界：

- 返回值聚合：`[2]*payload`、`[2][2]*payload`、`struct{A [2]*payload; B [2][2]*payload}`、`[1]struct{P *payload}`；
- 按值传入：上述 shape 再作为 byval 参数传入另一个 noinline callee；
- `>128B` 混合大结构（int / string / slice / interface / 指针 / 数组，`unsafe`-free，preflight 用 `reflect.Size` 断言 >128）；
- goret + byval + 普通参数混排：`func mixCall(seed, base, tag, [2]*payload, mixed, string) (int64, [2]*payload, mixed)`；
- 8 种泛型实例化 `Box[T]`：`T = [2]*payload`、`[2][2]*payload`、`agg`、`[1]one`、`mixed`、`*payload`、`[]*payload`、`any`（装箱聚合）。

每个 callee 内 `runtime.GC()` 后按同 size class 批量分配（poison，迫使刚释放的槽位复用），再解引用每个指针字段校验 canary；返回后 caller 再校验一次。每轮末尾用一次 GC 做 `weak.Make` 强引用断言：全部聚合仍由本地变量强引用（`runtime.KeepAlive` 延伸到 GC 之后）时 `weak.Value()` 必须非 nil；收尾时记录失活计数（metrics，不断言）。

与 u01 的分工：u01 只做堆对象字段/写屏障；本场景打“聚合按值、byval 参数、args/locals 边界、嵌套数组 FCA”，二者正交。

## 不变量

1. 每个 payload 的 `id/magic/aux` 与 `(seed,id)` 确定性公式一致（分配后 / callee GC 后 / caller 返回后三段校验），任何一次读取失配立即 FAIL；
2. 所有聚合 shape（含嵌套数组与泛型实例化）的每个指针字段在 callee GC + poison 复用之后仍指向正确对象；
3. `mixed` 的 string/slice/interface 字段内容与长度、`Pad` 哨兵在按值返回/传入后不变；interface 拆箱类型正确；
4. `mixCall` 的普通参数/返回值与 byval 聚合一致（checksum = `tag+len(s)`）；
5. weak 强引用断言：聚合被本地强引用时 `weak.Value() != nil`（对象被提前回收 = 漏标/UAF）；
6. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started == finished`；
7. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
8. 最后一行 `STRESS PASS x01_aggregate_statepoint`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 确定性 ID/种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 8 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

`.skip-envs` 含 `detector`：`efence=1` 逐对象 page 化，与本场景大量小 payload 不合，且小对象 stale read 在 efence 下未必 fault（探针靠 canary/weak）。

## 失败判读

- `canary mismatch ... id=... want=... magic=...`：对象已被回收/复用——聚合漏标或 FCA 抽取错误（重点看 `*.callee-post-gc` 与 `box.callee-post-gc`）；
- `nil payload`：byval 聚合中的指针在 GC 后丢失/被清零；
- `mixed.S=...` / `mixed.Iface=...` / `Pad corrupted`：按值拷贝路径损坏（大结构拷贝或 args/locals 边界）；
- `strong ref held but object collected`：weak 强引用断言失败——聚合仍可达却被回收（最直接的漏标判据）；
- `no worker progress`：watchdog 超时（GC 或校验卡死），附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- `mixed size=... want >128`：场景自身结构退化（仅 preflight）；
- 仅在 soak/window/clobberfree 下失败：漏标/UAF；base 也失败多为 shape 构造或校验逻辑错误。

## 运行

```bash
SUITE=stress FILTER=x01 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=x01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
SUITE=stress FILTER=x01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 BUILD_FLAGS=-race PROFILES=base ./run.sh
```

## 来源

- `docs/research/stress_extreme_survey.md:35`（x01 条目：A4/A5/A8、FCA 嵌套数组、8 种泛型）；
- 报告 R §2 `A4`（deopt 聚合参数）、`A5`（FCA）、`A8`（args/locals 边界）；
- `gollvm/passes/GoExtractFCAForGC.cpp:236-279`（261/277 `llvm_unreachable`：不支持的聚合抽取形态）；
- `gollvm/passes/GoFuncInfoHandler.cpp:101-190`（byval 参数/返回的 funcinfo 与 statepoint）。
