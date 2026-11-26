---
id: x02_localarray_liveness
priority: P0
covers: [L17, L11]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

局部指针数组 liveness：对准 go#82026 形态——局部 `arr := [N]T{...}`（T 含 `*payload`）的指针在下标非常量时可能被错误判定为死值/CSE 到寄存器，GC 落在 call 边界时对象被回收。每个 worker 每轮：

1. 构造 8 种 shape 矩阵：数组大小 `1/2/4/8` × 元素 `1/2` 个指针字段（`element1{p}`、`element2{p,q}`）；
2. 下标 `m` 由 `seed ^ round*常量 ^ shape` 派生（非常量、不可被优化为常量），call 前取 `q := &arr[m].p` 与值拷贝 `v := arr[m].p`；
3. `//go:noinline` callee 内 `runtime.GC()` + 同 size class poison 分配（迫使复用），callee 内先校验整组数组元素；
4. 返回后通过 `*q` / `v` / `arr[m].p` 三路读取并校验 canary（双路证实取址与值拷贝都未被错误折叠）；
5. 再经一个无参数 noinline `gcOnly` 做第二轮 GC（取址跨多轮保留），随后在 GC 后改写 `arr[m].p`，第三轮 GC 后验证新指针、字段别名 `*q` 与同元素第二个字段 `q`（element2）；
6. 另有 `held [8]element2` 数组在 worker 生命周期内保留取址，每轮重写 + 两轮 GC 校验（跨轮保留地址）。

探针：canary + `weak.Make` 强引用断言（局部数组/取址仍为强引用时 `weak.Value()` 必须非 nil；改写后的新指针同样断言）。收尾丢掉 held 强引用后 GC，把 last-round weak 失活数记入 metrics（不断言）。

与 u02/s04 的分工：u02/s04 打寄存器/接口持针与抢占窗口；本场景打局部指针数组 + 非常量下标 + 取址跨 call 的 liveness/zero point，不重复。

## 不变量

1. 每个 payload 的 `id/magic/aux` 与 `(seed,id)` 公式一致（callee GC 后 / 第二轮 GC 后 / 改写后 GC 后），任何失配立即 FAIL；
2. 8 种 shape 的 `*q`、值拷贝 `v`、`arr[m].p` 三路读取指向同一正确对象；改写后 `*q` 与 `arr[m].p` 都看到新对象，element2 的 `q` 字段仍为旧对象；
3. 数组其它下标元素在 GC 后无变化（callee 内整组校验）；
4. 返回的非常量下标 `m` 与 caller 用同一 `pick` 计算的期望一致（防止被错误折叠）；
5. held 数组跨轮重写 + 多轮 GC 后指针字段仍正确；
6. weak 强引用断言：局部数组/取址/held 数组持有时 `weak.Value() != nil`；
7. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started == finished`；
8. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
9. 最后一行 `STRESS PASS x02_localarray_liveness`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 下标/ID 派生种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 8 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

本场景不跳过 `detector`（固定 1 轮自检即用 `PROFILES=detector`）。

## 失败判读

- `canary mismatch ...`：对象已被回收/复用——局部数组漏标、取址/值拷贝 liveness 错误；
- `nil payload`：`*q` / `v` / `arr[m].p` 在 GC 后为空；
- `phase=index ... m=... want=...`：非常量下标被错误折叠/传播；
- `1x1/rewrite-after-gc`、`q-after-rewrite` 等失配：GC 后写回局部数组槽位的指针未被正确保活；
- `strong ref through local array lost` / `through retained array lost`：weak 强引用断言失败（漏标/UAF 最直接判据）；
- `no worker progress`：watchdog 超时（GC 或校验卡死），附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- 仅 window/clobberfree 下失败：漏标/UAF；base 也失败多为 shape/下标逻辑错误。

## 运行

```bash
SUITE=stress FILTER=x02 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=x02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
SUITE=stress FILTER=x02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=1 PROFILES=detector ./run.sh
SUITE=stress FILTER=x02 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 BUILD_FLAGS=-race PROFILES=base ./run.sh
```

## 来源

- go#82026（局部指针数组 + 非常量下标在 call/GC 边界的 liveness/CSE 问题）；
- `docs/research/stress_extreme_survey.md:36`（x02 条目：`arr := [2]T{...}`、`&arr[m].p`、noinline GC）；
- 报告 R 的 zero point 分析（`GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md` 约 3420–3454 行）；
- `go/src/runtime/stack_test.go` 中“指针在调用后仍被栈引用”的检查思路（canary + 显式 GC）。
