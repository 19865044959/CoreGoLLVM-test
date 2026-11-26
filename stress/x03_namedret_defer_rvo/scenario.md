---
id: x03_namedret_defer_rvo
priority: P1
covers: [L17, L04]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

命名返回值 + defer 改写 + RVO：对准 D19/P0#2——`func f() (r *Big)` 的命名返回槽在 deferreturn PC 处若缺 stackmap/pcdata，defer 内改写或 GC 会让 `r` 指向的对象被回收。每个 worker 每轮执行 12 个 op（`//go:noinline` 与可内联版本交替）：

1. `namedRet`：defer LIFO 改写 `r` 后 `runtime.GC()`（deferreturn 处 GC），另一 defer 做 `weak.Make` 强引用断言 + 全字段校验；
2. `namedStructRet`：命名大 struct（`Big`，144B）返回 + defer 改写 `r.A` 后 GC（命名 struct 返回槽的拷贝/RVO 路径）；
3. `bigByValue`：`Big` 按值返回 → 按值传入 noinline callee（callee 内 GC + poison），返回副本再校验；caller 改写副本 `A` 验证拷贝独立性；
4. `multiReturn`：if/else 两条返回路径返回不同对象，defer 中 GC 后按路径校验；
5. `panicNamed`：`panic(panicVal{p})` → recover → GC（recover 后 deferreturn 处）；命名返回 `r` 与 panic value 内的指针都要跨 GC 存活；
6. `deferChain`：三个 defer 依次改写 `r`（最后写者胜），中间 defer 内 GC；
7. 每个 op 的 defer 内做 `weak.Make` 强引用断言（命名返回槽/按值副本/panic value 持有时 `Value()` 必须非 nil）；轮末再对 caller 侧结果做一次额外 GC 的 weak 断言。

`Big` 含 5 个指针来源：`A`/`B` 直接指针、`S []*payload` 两元素、`Iface any` 装箱指针；每次 GC 后逐字段校验 canary。

与 u08 的分工：u08 校验 panic 展开帧内容；本场景打命名返回/RVO/deferreturn PC/多返回路径，触发路径不同。

## 不变量

1. `Big` 的每个指针字段（A/B/S[0]/S[1]/Iface）与 `N`/`Pad` 哨兵在 deferreturn GC 后与确定性构造一致（改写处用改写后的 id）；
2. 命名返回 `*Big` 在 defer 链改写与两次 GC 后仍指向最后写者的对象；
3. 命名返回大 struct 的值拷贝在 defer 字段改写 + GC 后所有字段正确（RVO/拷贝路径）；
4. 按值传入的 `Big` 副本在 callee GC + poison 后全部指针字段正确；caller 原值与返回副本互相独立（改副本不影响原值）；
5. panic/recover 后命名返回 `r` 与 panic value 中的 payload 都跨 GC 存活；panic value 类型断言正确；
6. 多返回路径按 `pick` 返回对应对象，defer 中按同一 `want` 校验；
7. `weak.Make` 强引用断言成立（命名返回槽/按值副本仍强引用时 `Value() != nil`）；
8. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started == finished`；
9. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
10. 最后一行 `STRESS PASS x03_namedret_defer_rvo`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 返回路径/ID 派生种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 8 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `canary mismatch ...` / `nil *Big`：命名返回槽或按值副本中的指针在 deferreturn GC 后被回收/清零（RVO/拷贝路径漏标）；
- `named result cleared before deferreturn`：defer 执行时命名返回已被破坏（stackmap/pcdata 错误）；
- `strong ref through named result lost` / `through byval copy lost`：weak 强引用断言失败（最直接漏标判据）；
- `panic value=...` / `panic-value-post-gc`：panic 记录/恢复路径的指针未被扫描；
- `copy-independence`：按值返回的拷贝别名错误（不该发生的共享）；
- `multiReturn ... want=...`：返回路径选择与 defer 视野内的 `r` 不一致；
- `no worker progress`：watchdog 超时（GC 或校验卡死），附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- `Big size=... want >128`：场景自身结构退化（仅 preflight）；
- 仅 window/clobberfree 下失败：漏标/UAF；base 也失败多为 defer 顺序或期望 id 逻辑错误。

## 运行

```bash
SUITE=stress FILTER=x03 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=x03 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
SUITE=stress FILTER=x03 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 BUILD_FLAGS=-race PROFILES=base ./run.sh
```

## 来源

- `docs/research/stress_extreme_survey.md:37`（x03 条目：named-return/RVO × deferreturn × return 处 GC）；
- 报告 R §2 `D19`（RVO）、P0#2（命名返回 + defer）；
- `gollvm/passes/GoReturnValue.cpp`（返回值/隐藏返回指针的 ABI 与 GC 信息）；
- `gollvm/passes/GoProcessDefer.cpp`（defer/deferreturn 的 pcdata 与帧处理）；
- `go/src/runtime/stack_test.go:244-328,868-929`（返回值/栈扫描相关检查思路）。
