---
id: y04_rvo_escape_addr
priority: P0
covers: [L11, L17]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

RVO/内联消拷贝 × 返回值字段地址导出逃逸。`func f() Big { var b Big; ...; return b }` 的内联版（期望消拷贝直接构造在调用方槽）与非内联版对照：

- **fNoinline**：匿名返回大对象（Big 528B，17 指针跨两个位图 word），fresh 短命节点，`//go:noinline` 对照版；
- **fInline**：同形状但字段写全来自预置池（每 worker 一行全局池，指针赋值不产生分配）——`go build -gcflags=-m` 证实**可内联**（`can inline fInline`）；内联 + RVO 消拷贝直接把聚合构造在调用方槽；
- **fDefer**：defer 修改返回值变体——defer 内改写局部聚合 `Ptrs[0]`（换成 fresh 节点）+ `gcGate()`（deferreturn 窗口：调用方槽此时尚未写入）；返回值必须在 defer 前求值（快照语义），RVO 若把局部聚合提升为结果槽导致改写泄漏进结果，快照校验即现形；
- **字段地址导出逃逸**：调用方取返回值字段地址 `&ret.Ptrs[0]` 存入 interface（逃逸 → 返回槽成为 address-taken 栈对象，其位图被测）→ `gcGate()` → 经导出指针校验——槽位图漏标即对象被回收、canary 失配；
- **返回后立刻 GC 再消费** + chan 直接送走变体 + weak 强引用探针；
- **脏栈铺垫**：dirtyRec（`d = 3 + i%96` 各层帧刷满短命指针 + depth 0 `gcGate()` 清扫）+ victimRec 同深度下降（同签名同 carrier → 帧尺寸一致，返回槽落在脏字上）；内联叶子的脏槽孪生 fDirtyInline（同签名写 fresh 节点）保证脏字形态。

## 与 x05 命中形状的关系与差异

- x05 是匿名返回槽 + panic 载体；本场景打**RVO/内联消拷贝路径**（D19 风险点）：内联后聚合直接构造在调用方槽——写入窗口与调用方栈图交织，且字段地址导出使返回槽成为栈对象（A9 stackObjectRecord 相邻形态）；deferreturn 窗口（调用方槽尚未写入时被 GC）是 x05 未覆盖的第三窗口；
- 与 x03（命名返回 + defer 改写）的差异：本场景是**匿名**返回（返回值在 defer 前求值——快照语义可被确定性校验），且内联/非内联双版本对照；
- 判据相同：官方 Go 恒 PASS；崩溃 = 编译器 bug（返回槽未入口清零/栈对象位图漏标/RVO 语义破坏）；`FAIL canary/指针同一性/快照` = 确定性 UAF 或拷贝语义破坏。

## 不变量

1. fNoinline 返回值分字段 canary 与 `(seed,base)` 公式一致；字段地址导出后 GC，经导出指针读取仍完整；
2. fInline 返回值指针与预置池**同一**（`Ptrs[i] == &gPool[poolOff+i]`）且 canary 一致——拷贝/RVO 语义破坏即现形；
3. fDefer 返回值 `Ptrs[0]` 为 defer 前快照（id=base，不得是 defer 改写的 base+777），其余字段完整；
4. chan 值跨期间全部 GC 存活，drain 校验完整；weak 强引用断言成立；
5. 深度抖动覆盖 `[3,98]`；固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮，chan 收尾为空，escape 接口清空；`started == finished == workers`；
6. 心跳持续增长，watchdog 6 个周期无进展 → FAIL（附全栈）；
7. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
8. 最后一行 `STRESS PASS y04_rvo_escape_addr`——官方 Go 恒 PASS；**崩溃 = 编译器 bug**，PASS = 本轮未触发。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 确定性 ID/种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 60 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径（fatal error 现场） |

`.skip-envs` 含 `detector`：`efence=1` 破坏同 size class span 复用语义且大量小对象必然内存爆炸，本场景判据不依赖 efence。

## 失败判读

- 进程崩溃（`found bad pointer in Go heap` / `pointer ... to unallocated span` / `found pointer to free object` / `marked free object`）：**返回槽未入口清零/栈对象位图漏标**，某 gate GC 扫到脏槽——直接 FAIL；
- `canary mismatch` / `nil *node`：对象被提前回收或复用——**确定性 UAF**；
- `Ptrs[i]=... want=&pool[...]`：内联/RVO 拷贝语义破坏；
- `y04.defer.snapshot` canary 失配：defer 改写泄漏进返回值（RVO 提升结果槽语义破坏）；
- `strong ref held but collected`（weak）：返回槽漏标；
- `chan 残留 != 0` / `escape 接口未清空`：会计损坏；
- `no worker progress`：watchdog 超时，附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- 只在 `window` 失败：漏标/并发窗口；`detector` 崩溃：释放对象被再次访问（UAF）。

## 运行

```bash
SUITE=stress FILTER=y04 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=y04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=300 PROFILES=window ./run.sh
SUITE=stress FILTER=y04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=y04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 PROFILES=base ./run.sh
```

## 来源

- `stress/x05_goexit_panicframe`（匿名按值返回槽命中点）、`stress/x03_namedret_defer_rvo`（命名返回 + deferreturn 窗口）、`stress/g01_goret_dirty_slot`（dirtyRec/victimRec 手法）；
- `docs/research/stress_extreme_survey.md` R:D19（RVO 消拷贝路径）、A9（stackObjectRecord 相邻形态）；
- `docs/research/stress_batch4_design.md` y04 条目；DIMENSIONS.md L11（编译期优化）与 L17（栈映射）。
