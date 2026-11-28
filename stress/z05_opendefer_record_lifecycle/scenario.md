---
id: z05_opendefer_record_lifecycle
priority: P0
covers: [L17, L18, L04]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

open-coded defer 记录生命周期的定向压力（defer 记录本体、panic 展开路径、递归 recover 后复用）。

递归函数 `rec3` / `rec5` / `rec8` 的函数体只含 3 / 5 / 8 个**简单 defer**（无循环、无闭包、无动态分支），每个 defer 都是 `//go:noinline` 的指针参数调用 `checkNode(p, tag, want, depth)`——编译器必须生成 open-coded defer 记录（defer 参数存于栈帧内的记录槽）。

每轮（每 worker）：

1. `chain(0)` 递归到 `chain(2)`：每层先跑一条深度 200 的 rec 链，最底层 `panic(panicPayload)`；payload 是大 struct（24 个 `*node`、字符串、8 个 uint64 words），由该层 `defer func(){ recover(); 校验 payload }` 捕获；内层 recover 返回后父层再跑一条 rec 链、再 panic——形成 3 层嵌套 recover 链（模仿 go#37782 的递归 panic/recover，含 recover 后再入）；
2. `checkNode` 校验 defer 参数指针 `p` 的身份（`p.id == depth`）与 canary（`p.v == nodeWant(seed,depth)`），并按 `(depth, deferIdx)` 矩阵计数；
3. 每轮按 seed 选一个 `(tag, depth)`，在 `checkNode` 内触发 `runtime.GC()`——GC 发生在 panic 展开/deferreturn 中间（变体，`r%3==0` 时在 depth=0 最底层触发）；
4. 回合末 `runtime.GC()` 后校验全部 node canary，并再跑一次浅链（换 defer 变体）覆盖“recover 后复用/回收 defer 记录”的形态。

矩阵总计断言：每条链的每一帧、每个 defer 恰好执行一次（每轮两遍共 6 条链，故每个条目 = 3 次）；`recoveries == 3/遍`。多 worker 并发同时展开（go#37688 panic 路径）；每轮少量 GC + 独立 gcChurn 并行；不使用 `runtime.Goexit`（x05 覆盖）。window profile（STW、GOMAXPROCS=1）必须跑，放大 panic 路径窗口。

# 对应历史案例

`docs/research/stress_history_bugs.md` 第 5、6、8 条：

- **go#37688（2020）**：open-coded defer panic 路径 `newdefer` 把 defer 记录挂到 g0 链表，陈旧记录保活已回收的 defer 结构；`freedefer` 不清零 → GC 扫到坏指针（`bad pointer`/GC 崩溃）；
- **go#37782/#37664（2020）**：递归 panic/recover 后陈旧的 open-coded defer 条目残留链表，执行时 SEGV；
- **go#44623（2021）**：open-coded defer 的闭包/参数槽 liveness 错误（总被视为 live → 泄漏；反向省略即 UAF）。
- 覆盖缺口表第 5 行“open-coded defer 记录生命周期”——u08/x03/x05 只测展开帧内容，不测 defer 记录本身。
- 关联维度：L17（栈映射/安全点，defer 参数指针槽）、L18（GC 生命周期）、L04（调度/栈，深递归与展开）。

# 不变量

1. 每个 `checkNode` 的 defer 参数指针 `p == nodes[depth]`、`p.id == depth`、`p.v == nodeWant(seed,depth)`、`tag=="z05"`；
2. `(depth, deferIdx)` 矩阵总计：3、5 或 8 个有效标签各 3 次/轮，无效标签 0 次；每条 rec 链的每帧每个 defer 恰好执行一次；
3. 每轮恰好 3 次 recover（两遍共 6 次），每次 recover 的 payload `level` 正确；
4. panic payload 的 24 个指针槽仍指向原 node 且 canary 完好，words 递推链与字符串未被改写（展开期间被 `_panic` 记录保活）；
5. 每轮显式 GC 后全部 node canary 完好；
6. 固定模式每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；每 worker `started==finished`；
7. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
8. 末行 `STRESS PASS z05_opendefer_record_lifecycle`。

# 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16），并发同时展开 |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL 并附全栈 |
| `UAF_SEED` | 1 | seed；决定 canary、defer 变体顺序、defer 内 GC 位置 |
| `STRESS_FIXED_ROUNDS` | 16 | `seconds=0` 时每 worker 轮数 |
| `STRESS_DEPTH` | 200 | rec 链深度（8–512）；每帧 3/5/8 个 open-coded defer |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

# 失败判读

- `defer 参数指针为 nil` / `指向错误对象` / `参数 canary 错误`：defer 记录中的指针参数槽被错误回收或覆写（go#44623 反向 UAF、go#37688 freedefer 不清零）；
- `defer 执行次数=... want=...`：open-coded defer 记录丢失/重复执行（go#37782 陈旧条目、panic 路径链表残留）；
- `recover 次数` / `rec 链未触发 panic`：展开链断裂或记录被复用；
- `payload.nodes[...] 丢失` / `words[...]` / `payload.str 被改写`：`_panic` 记录未保活大值 payload（跨展开 GC 提前回收/覆盖）；
- `node[...] corrupted after panic/recover storm`：存活 node 被回收或内容被破坏；
- `bad pointer`/`freedefer`/GC 崩溃（runner crash-output/core）：defer 记录链表含坏指针；
- 只在 `window` 失败：STW/单 P 放大展开与 deferreturn 窗口；
- `goroutines baseline ... leak`：defer/recover 路径泄漏 goroutine；
- `无 worker 进展`：展开卡死，附全栈。

# 运行

```bash
SUITE=stress FILTER=z05 STRESS_SECONDS=3 PROFILES=soak,window REPEAT=1 TIMEOUT=180 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=z05 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree REPEAT=1 TIMEOUT=180 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
```

## 来源

- go#37688（freedefer/defer 记录与 g0 链表）、go#37782/#37664（递归 panic/recover 陈旧条目）、go#44623（defer 参数/闭包槽 liveness）；
- `go/src/runtime/panic.go`（`_defer`/`freedefer`/`deferreturn`）、`go/src/cmd/compile/internal/walk/defer.go`（open-coded defer 生成条件）；
- `docs/research/stress_history_bugs.md` §1 第 5/6/8 条、§2 缺口表；
- `docs/research/stress_batch5_design.md` z05 条目与“defer 执行计数与参数校验”探针。
