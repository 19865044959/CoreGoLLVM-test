---
id: z06_cgo_newthread_stackgrowth
priority: P1
covers: [L09, L04, L18]
requires: [cgo]
default_seconds: 60
max_workers: 8
---

# 场景

新 C 线程 + 回调内栈增长（需 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`，C 定义在 `storm.c`）。

每 worker 每轮调用 C `c_spawn_one(cb)`：C 侧**新建一个 pthread**（`pthread_create`），线程存活期仅本次回调，回调返回后立即 `pthread_join` 回收——**不是**常驻线程池。线程内以函数指针调用 Go 侧 `//export goCb`，使每次回调都落在一个新 C 线程对应的 extra M/G 上。

`goCb` 内：

1. 由 `seed/iter` 派生递归深度 `depth ∈ [800, 2000]`（`depthFor`，父侧可独立复算）；
2. 深递归 `deepFrame`：每层一个局部 `*node` 指针槽 + `[4]*node` pad，每 256 层执行一次 `runtime.GC()`，强制回调所属 G 的栈段反复 morestack 并接受 GC 扫描；
3. 递归展开时逐层校验本层指针与内容 canary；
4. 返回 `deepFrame` 和 + 参数往返项；C 线程把返回值带回，父侧与纯 Go 函数 `expectedCb` 的计算结果比对。

独立 goroutine 提供 GC churn（持续分配 + 显式 GC），与线程创建/回调执行重叠。`runtime.NumCgoCall()` 只记录不断言。线程创建严格每轮 1 个，轮数由 `STRESS_FIXED_ROUNDS`/`STRESS_SECONDS` 控制，避免资源耗尽；`pthread_create/join` 失败即带上下文 FAIL。

## 对应历史案例

- **gccgo gcstack 补丁（2019，stress_history_bugs.md #14）**：新建 C 线程回调创建的 extra G 保留了旧 gcstack（指向已 unmapped 的栈段）→ GC 扫描已释放内存。本场景每轮丢弃线程、逼运行时复用 extra M/G 并重建栈描述，深递归确保复用时栈段确实发生增长。
- 关联 go#57170（cgo 帧坏指针，库级）与 n07/x07（常驻线程回调）的缺口：x07 的线程持续存在，不覆盖“线程每轮新建 → extra G 复用”的路径。

## 不变量

1. 预检单次回调返回值 = 纯 Go 期望值（`//export` 符号、函数指针、ABI 往返一致）；
2. 每轮回调返回值 = `expectedCb(seed, iter)`（深递归校验和 + 参数往返项）；
3. 递归中每次 GC 后、以及展开时每层 `local/pad[0]/pad[3]` 指针与内容不变；
4. 线程创建数 == 完成轮数（每轮恰好 1 条新 pthread，join 成功）；
5. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮，`started == finished`；
6. 心跳持续增长，watchdog 无停滞；收尾 goroutine 回基线（+4 容差）；
7. 最后一行 `STRESS PASS z06_cgo_newthread_stackgrowth`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 2 | worker 数（≤8），每 worker 每轮 1 条新线程 |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 种子（深度派生 + 失败输出） |
| `STRESS_FIXED_ROUNDS` | 8 | `seconds=0` 时每 worker 轮数（= 线程数/worker） |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- 构建失败：确认 `CGO_ENABLED=1`、gcc 可用、`BUILD_MODE=module`；
- `phase=preflight: callback returned ...`：`//export` 签名与 `storm.c` extern 声明不一致，或函数指针传递失败；
- `phase=spawn ... pthread_create failed`：线程资源耗尽（检查轮数/系统线程上限）；
- `phase=spawn ... pthread_join failed`：线程回收异常；
- `phase=spawn ... C returned ... want=...`：回调返回值穿越 C 线程往返时损坏（调用约定/寄存器/ABI）；
- `phase=callback-deep|callback-gc ... corrupted`：回调递归帧在栈增长或 GC 后损坏，或新线程对应的 extra G 使用了陈旧栈描述（gcstack 类，重点）；
- `phase=watchdog`：回调/线程创建卡死，附全栈；
- `phase=final ... goroutines baseline`：收尾 goroutine 泄漏。

## 运行

```bash
CGO_ENABLED=1 SUITE=stress FILTER=z06 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
CGO_ENABLED=1 SUITE=stress FILTER=z06 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base,clobberfree ./run.sh
# 手动构建（BUILD_MODE=module）：
cd stress/z06_cgo_newthread_stackgrowth && CGO_ENABLED=1 go build -o /tmp/z06 . && /tmp/z06
```

`requires: [cgo]`：不进默认 CI；`CAPS` 不含 `cgo` 时 runner 自动跳过。

## 来源

- [gccgo gcstack for extra G](https://patchwork.ozlabs.org/project/gcc/patch/CAOyqgcU-CDQh+LPJPEZVQMhaHE1Q8kT+ahAoP2+Er5EAzOXPLg@mail.gmail.com/)
- `go/src/runtime/cgocall.go`（needm / cgocallback / extra G 路径）
- `stress/x07_cgo_panic_deepstack`（回调内深递归 + GC 骨架）、`stress/n07_cgo_storm`（C 线程回调与 module 构建）
