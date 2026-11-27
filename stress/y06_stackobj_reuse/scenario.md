---
id: y06_stackobj_reuse
priority: P0
covers: [L17, L04]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

A9 stackObjectRecord 正常流复用（此前从未覆盖；x05 的 dead object 是 panic 保活形态，这里是**正常流**中的 stack object 复用）：

- **大局部聚合 + noinline + defer 取址**：`deepFrames` 每层帧内 3 个 addrtaken 局部（`Big` 264B + `[8]*node` + 混合 `obj3T`，不同 offset），全被同一 `defer func(){ use(&local) }()` 闭包取址捕获——逃逸分析不逃逸（`-m` 验证：func literal does not escape），形成 stack object；递归中的 defer 记录在堆上持有指向栈内的指针，GC 必须按 stackObjectRecord 精确扫描；
- **跨 goroutine 校验**：depth 0 的 GC 是 gcChurn/driver 另 goroutine 触发的 STW（扫所有 worker 栈）；各层节点**只经本层栈对象可达**，unwind 时逐层 defer 校验 canary + weak 探针（漏 stackObjectRecord → 节点被回收 → 立即现形）；抽样层 defer 内再 GC；
- **收缩**：浅返回后 `shrinkFrame` 在 channel 上阻塞（浅帧内仍有 defer 取址局部），driver 执行 GC/FreeOSMemory → shrinkstack/copystack 搬移栈；恢复后立即校验 + defer 校验（captured 指针被 adjust 正确）；
- **同栈区域复用**：收缩后再进同深度深调用，复用收缩后的栈区域（新 stack object 与旧记录不串）；
- **morestack 交错**：深递归（含 defer 局部）多次增长/搬移栈，pending defer 的捕获指针与 stack object 位置被 copystack adjust；
- **逃逸控制变体**：`box.iface = &local` 存入逃逸接口（escape 分析堆分配——控制组），checker goroutine 跨 goroutine 校验；
- `debug.SetMaxStack(64MiB)` 限深；深度抖动 d=3+r%96。

差异说明：不读取已回收栈内存（UB）；「旧帧区域」判据一律走合法引用（defer 捕获指针 / weak 探针 / 逃逸接口）或「无崩溃」。`STRESS_CRASH_FILE` 捕获 runtime 崩溃文本。

来源：`docs/research/stress_batch4_design.md` y06；`docs/research/stress_extreme_survey.md` A9；`runtime/stack.go`（stackObjectRecord/adjustpointer）、`runtime/mgcmark.go`（栈扫描）；u09 收缩 driver 手法。

## 不变量

1. `deepFrames` 每层 3 个 stack object 在 depth 0 GC 后仍存活：unwind 时每层 defer 的 canary/words/seq 全部一致，weak 探针非 nil（`frames == d+1`）；
2. 收缩后（driver GC/FreeOSMemory）`shrinkFrame` 的 captured &local/&obj2 在恢复时与 defer 中校验一致（copystack 重定位正确）；
3. 复用阶段（收缩后再进深调用）新 stack object 校验与第一阶段相同；
4. 逃逸变体跨 goroutine 校验一致（`escapedChecks == rounds`）；
5. 每轮 `shrink_cycles==2`、`weak_checks==3`；固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；
6. 收尾 quiesce 后 goroutine 回基线（+4 容差）；`Big` 尺寸恒 264B；
7. 最后一行 `STRESS PASS y06_stackobj_reuse`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | seed，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 8 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `nil *node (UAF/栈对象漏扫描)` / `canary 失配`：GC 未按 stackObjectRecord 扫描栈对象，节点被回收后 defer 读到已释放对象——本场景的核心信号，最敏感于 `clobberfree`/`window`；
- `weak 探针失效`：栈对象漏扫描（weak 不受 clobber 影响，任何 profile 下都现形）；
- `收缩后 canary 失配（栈搬移漏重定位）`：copystack 未 adjust stack object 内指针/捕获指针；
- `栈对象被改写`（words/seq）：复用栈区域时旧对象残留/新对象未初始化；
- `shrink_cycles ... want`：收缩点未发生（driver/worker 协议损坏）；
- `6 个心跳周期无进展`：卡死（附全栈）；`goroutines baseline ... leak`：defer/闭包泄漏；
- 只在 `window` 失败：并发窗口；`detector` 崩溃：释放对象被再次访问（UAF）。

## 运行

```bash
SUITE=stress FILTER=y06 STRESS_SECONDS=3 PROFILES=soak,window REPEAT=1 TIMEOUT=120 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=y06 STRESS_SECONDS=3 PROFILES=layout REPEAT=1 TIMEOUT=120 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=y06 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base,clobberfree REPEAT=1 TIMEOUT=180 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
```

## 来源

- `docs/research/stress_batch4_design.md` y06；`docs/research/stress_extreme_survey.md` A9（stackObjectRecord 从未覆盖）；
- `go/src/runtime/stack.go`（stackObjectRecord、adjustpointer）、`mgcmark.go`（栈扫描路径）；
- `stress/u09_stack_shrink_cycles`（收缩 driver 协议）、`stress/x05_goexit_panicframe`（Big 264B 形状）。
