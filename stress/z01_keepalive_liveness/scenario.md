---
id: z01_keepalive_liveness
priority: P0
covers: [L18, L17, L05]
requires: []
default_seconds: 60
max_workers: 8
---

# 场景

**KeepAlive 本身是被测目标**（现有 58 个场景只把它当工具）。每轮以 `3+round%64` 的递归帧抖动改变 GC 时的栈布局与栈深度，并在每帧保留一个活指针槽；随后执行四种形态：

- **形态 A（可重物化栈地址）**：`var b box`（`box{p *node; x int64}`，轻到编译器可把 `&b` 当可重物化地址）→ 只经 `b.p` 访问 `p` → `runtime.GC()` → `runtime.KeepAlive(&b)` → 读 `b.p` 并校验 canary。对应 go#30476：KeepAlive 作用于栈对象地址时若被忽略，仅由该栈对象持有的堆对象会被提前回收。
- **形态 B（last-use 缩短）**：`p.child` 的最后一次直接使用在 GC 前；GC 后只经派生指针 `q := p.child` 使用，`q.back` 指回 `p`，构成 `q→child→p` 链，`p`/`q` 的 finalizer 均不得提前触发。对应 go#22458 spill 槽/liveness 缩短类问题。
  注意：Go 规范允许“最后一次使用是纯值读取”的对象在随后 GC 中被回收；对 `q := p.v`（纯 int 派生值）断言 `p` 不得终结会在官方 Go 1.24.1 上误报（已本地实测 finalizer 会执行）。因此这里用必然保活的 back-pointer 链构造合法程序，断言的是“派生指针跨 GC 保活其引用链”这一真实不变量。
- **形态 C（多栈对象链）**：`var arr [8]box`，每字段指向不同堆对象；两次 `runtime.GC()` 后 `runtime.KeepAlive(&arr)` 一次覆盖全部字段，逐字段校验内容、逐对象校验 finalizer/weak。
- **形态 D（defer 中 KeepAlive）**：`defer runtime.KeepAlive(&b)` 是函数返回时最后执行的语句，中间夹 3 轮 GC；`b.p` 只在 GC 前读取，GC 后只经 finalizer/weak 探针验证。对应 go#16249 类“defer/结果参数 liveness”形态。

每个堆对象注册 **finalizer**（`released==0` 期间执行 = 提前终结 = FAIL）与 **weak.Make** 探针（持有期 `Value()==nil` = 对象已被回收 = FAIL）。finalizer 只做原子计数 + cap-1 channel 非阻塞投递，检查点同步非阻塞读，不阻塞运行时终结器 goroutine。每个对象在完成校验后置 `released=1`，此后终结才合法。

独立 GC churn goroutine 与每轮显式 GC 并发；`GOMAXPROCS=1` 时 churn 每次 `Gosched()+Sleep(1ms)` 避免饿死 worker/watchdog。收尾用 4 个“立即失活”探针做终结器机制正向健全性检查（15s 只是失败上限，不是时延断言）。

## 对应历史案例

| 历史案例 | 本场景对应 |
|---|---|
| [go#30476](https://tip.golang.org/test/fixedbugs/issue30476.go) KeepAlive 未保活栈对象 | 形态 A / D |
| [go#22458](https://go.googlesource.com/go/+/6fc1242ea8f8bbacd88cb834ef61dbe9e84a8514) KeepAlive 必须引入 spill 槽的 use | 形态 B / D |
| [go#16249](https://tip.golang.org/test/fixedbugs/issue16249.go) defer 场景 liveness 计算错误 | 形态 D（defer 中 KeepAlive） |

## 不变量

1. 形态 A：GC + `KeepAlive(&b)` 后 `b.p` 仍为原对象且 `id/v/tag` canary 正确；
2. 形态 B：GC 后派生指针 `q` 及 `q.back` 内容正确，`p`/`q` 的 finalizer 均未触发；
3. 形态 C：`KeepAlive(&arr)` 后 8 个字段全部存活、内容正确；8 个 finalizer 均未触发；
4. 形态 D：多轮 GC 期间（`defer KeepAlive(&b)` 尚未执行）对象不被回收，finalizer 不触发；
5. 所有对象在 `released==0` 期间 `weak.Value()!=nil` 且 finalizer 计数为 0；
6. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮，`started==finished`；
7. 收尾“立即失活”探针的 finalizer 在失败上限内实际执行（探针有效性）；
8. goroutine 回基线（+6 容差）；末行 `STRESS PASS z01_keepalive_liveness`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 2 | worker 数（≤8） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 种子，失败行带出 |
| `STRESS_FIXED_ROUNDS` | 8 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

每轮 4 种形态共 12 个 finalizer/weak 探针（A 1 + B 2 + C 8 + D 1）。

## 失败判读

- `phase=A/post ... content corrupted after GC`：`KeepAlive(&b)` 未能保活 `b.p` 指向对象（可重物化地址问题）；
- `phase=B/child|back ... corrupted`：派生指针链在 GC 后丢失/损坏（last-use 缩短错误）；
- `phase=C/post ... slot=...`：`KeepAlive(&arr)` 未覆盖数组字段或某字段对象被回收；
- `phase=D/premature|notify|weak`：defer 中 KeepAlive 未生效，对象在函数返回前被回收/终结；
- `phase=*/weak ... Value()==nil`：对象仍被合法引用却被回收（最直接的提前回收判据）；
- `phase=*/premature` / `notify`：finalizer 在 `released==0` 期间执行（提前终结）；
- `jitter ... live stack slot corrupted`：递归帧中的活指针槽被破坏；
- `final: 已失活 probe 的 finalizer 未在 15s 内执行`：终结器机制失效/饥饿（探针自身无效）；
- `goroutines baseline ... leak`：辅助 goroutine 泄漏；`no progress`：watchdog 超时。

## 运行

```bash
SUITE=stress FILTER=z01 STRESS_SECONDS=3 PROFILES=soak,window REPEAT=1 TIMEOUT=120 ./run.sh
SUITE=stress FILTER=z01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=z01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 PROFILES=base ./run.sh
```

## 来源

- `docs/research/stress_history_bugs.md` #2/#3/#4 与 §2“liveness / KeepAlive 类”缺口；
- `docs/research/stress_batch5_design.md` z01；
- `go/test/fixedbugs/issue30476.go`、`issue22458.go`、`issue16249.go`；
- `runtime/mfinal_test.go`、`src/weak/pointer.go`（持有期 `Value()!=nil` 语义）。
