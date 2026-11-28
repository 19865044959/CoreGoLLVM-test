---
id: z07_zerorange_mixed_frame
priority: P0
covers: [L17, L11, L05]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

zerorange 清零区间与大帧栈增长（帧估算 39,792B > 32KB）。

`mixedFrame` 混入 19 组共 22 个局部变量（含增长后写入的 3 个）：`[16]*node` 指针数组、`[8]string`/`[16]string` 字符串数组、slice、`interface{}`、`map[int64]*node`、`[6]` 嵌套 struct 数组（内含 4 指针 + string + int + bool）、`[64]bool`、`[32]int64`、`[4096]byte`、`[1024]uint64`、`[512]zPair`、`[2048]int64`、`[8]interface{}`、`[64]float64`、`[32]complex128`、`[64]int16`、`[128]int8`、`[64]uint32`。

- 11 个 seed 派生分支位（`mask`）沿不同分支做**部分**初始化（指针数组偶数/奇数槽分开、数组只初始化前半/前 3 项等），其余区间保持未初始化 → 编译器对多个未初始化区间发射 zerorange/memclr；
- 每轮 `mask` 在“单 bit（逐个区间形态）/ 全 1 / 全 0 / seed 随机”间轮换；
- 函数入口注册 `defer w.checkMixedFrame(..., &pa, &ps, &sl, ...)`，返回时通过栈指针再读这些大帧局部；
- 函数中部调用 `growDeep`（默认 512 层、每层带 48 个指针槽 + 64 个 uint64，递归返回后继续使用旧帧槽）强制 morestack 栈增长/复制整个大帧；
- 增长前、增长后各一次 `runtime.GC()` 并全量校验：
  - a) 未走分支的局部 == 零值（Go 语义保证，读它们是合法的）；
  - b) 已初始化局部的 canary 完好（指针数组/字符串/struct 数组/标量数组逐项精确比对）；
  - c) 非 nil 指针槽均指向 `nodes[idx]` 且 `id/v/tag` canary 正确（无垃圾指针）；
  - d) 增长后新写入的 `postN/postW/postS` 精确校验；
- 每轮另跑 `loopBigFrame`：每次迭代在循环体内声明 32KB 大局部数组（`[1024]zPair` + `[2048]int64`），先全量校验为零（每轮迭代重新零化），再部分写 canary，校验尾部未初始化区间仍为零、指针槽合法；隔轮 GC 后复验。

所有读取都在规格允许的范围内（未初始化局部读零值是合法的），不制造 UB。独立 gcChurn 并发。

# 对应历史案例

`docs/research/stress_history_bugs.md` 第 10 条：

- **go#75477（2022）**：mips64 `zerorange` 漏了 +8 偏移 → 清零区间错位，指针槽盖不全，GC 报 `bad pointer in frame main.invoke ... 0x7`；
- 覆盖缺口表第 6 行“zerorange 清零区间（混合类型大帧、多分支条件初始化）”：x05/y01 依赖“未清零”制造脏栈，但从不校验**该清零的区间确实清零且不多不少**；
- 关联维度：L17（栈映射/栈复制）、L11（编译期优化：帧布局/清零代码生成）、L05（大帧分配与 GC）。

# 不变量

1. `mask` 未置位组的所有局部 == 零值：指针/字符串/slice/interface/map 为 nil 或空，标量/数组为 0；
2. `mask` 置位组的局部 == 精确 canary：指针数组按槽位公式、字符串按数组下标、嵌套 struct 按元素、标量数组逐项；
3. 所有非 nil 指针槽 `== nodes[idx]` 且 `id==idx`、`v==nodeWant(seed,idx)`、`tag=="z07"`（非垃圾）；
4. 增长前 / 增长后 / defer 返回时三次校验结果一致（栈复制正确搬运大帧与指针位图）；
5. 增长后新写入的 `postN/postW[4]/postS` 不受栈复制影响；
6. `loopBigFrame` 每次迭代：初始全零、部分写入 canary 正确、尾部保持零、指针合法，GC 后复验一致；
7. 固定模式每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；每 worker `started==finished`；
8. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
9. 末行 `STRESS PASS z07_zerorange_mixed_frame`。

# 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤32） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL 并附全栈 |
| `UAF_SEED` | 1 | seed；决定 canary、分支 mask、字符串/节点内容 |
| `STRESS_FIXED_ROUNDS` | 16 | `seconds=0` 时每 worker 轮数 |
| `STRESS_GROW_DEPTH` | 512 | 中部深递归层数（16–4096），强制 morestack 栈复制 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

# 失败判读

- `未初始化分支 ... 非零值`：zerorange/memclr 漏清或清零区间错位（go#75477 类），window/soak 下更敏感；
- `... canary 损坏` / `pa[...] 指针丢失` / `pairs[...].key`：已初始化区间被覆盖或栈复制丢内容；
- `指针槽垃圾/内容损坏`：指针槽含非 0x7 类垃圾值（`bad pointer in frame` 前兆）或对象被提前回收；
- `pre-growth` 失败：函数序言清零/初始化即错；`post-growth` 失败：morestack 栈复制的大帧指针位图错误；
- `defer` 阶段失败：defer 记录中的栈指针在增长后失效（L17 直接信号）；
- `增长后新写入 ... 损坏`：栈增长后新写入局部被 GC/复制破坏；
- `loopBigFrame ... 未初始化 ... 非零`：循环体重零化缺失；
- 只在 `detector` 崩溃：释放后访问/野指针（UAF）；
- `bad pointer in frame`/GC 崩溃：runner crash-output/core 记录；
- `rounds ... want ...` / `started/finished` 不等 / `goroutine baseline ... leak` / `无 worker 进展`：worker 提前退出、泄漏或卡死（附全栈）。

# 运行

```bash
SUITE=stress FILTER=z07 STRESS_SECONDS=3 PROFILES=soak,window REPEAT=1 TIMEOUT=180 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=z07 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=1 PROFILES=detector REPEAT=1 TIMEOUT=180 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=z07 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree REPEAT=1 TIMEOUT=180 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
```

## 来源

- go#75477（mips64 zerorange +8 偏移错位，`bad pointer in frame`）；
- `go/src/cmd/compile/internal/ssagen/ssa.go`（zerorange/memclr 生成）、`go/src/cmd/compile/internal/ssa/_gen/AMD64.rules`（duffzero/memclr）；
- `runtime.morestack` / `runtime.copystack`（大帧栈复制与指针位图）；
- `docs/research/stress_history_bugs.md` §1 第 10 条、§2 缺口表；
- `docs/research/stress_batch5_design.md` z07 条目与“zerorange 三态校验”探针；
- 相邻场景 x05/y01（脏栈制造）、u09（栈收缩）——均不校验清零区间本身。
