---
id: x10_inline_tailcall_pcdata
priority: P0
covers: [L17, L11]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

inline/musttail 尾调用 pcdata 边界压测（P0）。现有场景的 GC 注入都在阶段/轮边界，没有一个把 GC 放进“函数末尾最后一条 call 之后”的 pcdata 区间，也没有 method expression/method value/interface wrapper 与 deferreturn 的对照。

每个 worker 每轮执行 10 个用例：

1. **inline wrapper 链（tail/mid）**：`chainA→chainB→chainC→work`，每层分配并持有一个局部指针，调用下一层返回后仍继续使用该指针与下一层返回值；`work` 首行 `runtime.GC()`（tail：GC 恰在最后一条 call 之后），或在中部 GC 后再产生一条 noinline call 与指针使用（mid）。inline 版不加指令，`-m` 验证 `chainA` 被内联进调用者、`chainC` 被内联进 `chainB`（`chainB` 因末端 noinline call 的预算保留为帧）；noinline 对照逐层 `//go:noinline`，三层均为独立帧。
2. **method expression / method value**：`(*svc).callTail` 与 `s.callTail()`（method value thunk，可能 musttail）都走同一 wrapper 形态。
3. **interface 派发**：两个实现 + noinline `pick` 动态选择，阻止去虚化；tail/mid 各一条。
4. **deferreturn**：defer 闭包在 return 之后（deferreturn 处）执行 `runtime.GC()`，GC 后继续读取/替换捕获的命名返回值；另一用例在 deferreturn 闭包内调用 `workMid`（GC 位于 deferreturn 链上的调用内）。

所有节点带确定性 canary（`nodeV(seed,id)`）；每个 wrapper 在调用返回后、work 在 GC 前后都校验 id/canary，失配立即 FAIL。固定轮数下 10 个用例全部执行并逐一计数，避免只跑部分形态。

## 不变量

1. work 在 GC 后仍读到参数 p/a/b/c 的合法 canary 与 id，返回值为四者 canary 的 XOR；
2. 每个 wrapper 在下一层返回后仍能读到自己的局部指针与返回值（调用点 stackmap 必须保活局部/参数槽）；
3. method expression、method value、interface 派发三条路径与直接链的返回值一致；
4. deferreturn 处 GC 后，命名返回值与捕获指针内容不变，defer 对返回值的替换生效；
5. 固定轮数时每个用例恰好执行 `STRESS_FIXED_ROUNDS` 次，`rounds == fixed`；任何用例未执行不得 PASS；
6. `started == finished`，收尾 goroutine 回基线（+4 容差）；
7. 最后一行 `STRESS PASS x10_inline_tailcall_pcdata`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 种子，失败行带出 |
| `STRESS_FIXED_ROUNDS` | 16 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `phase=x10.work_tail.*` / `phase=x10.work_mid.*` canary/id 失配：GC 后参数指针被回收或改写——inline/musttail 调用点 stackmap 漏根（重点看 tail，即“最后一条 call”位置）；
- `phase=x10.chain_*_inline/...` 或 `x10.method*/x10.iface*` 失配：wrapper 内局部指针未在调用点保活（inline 合并帧与独立帧都要看）；
- `返回值为...`：数据流被 GC/内联改写；
- `phase=x10.deferreturn.*`：deferreturn 边界 stackmap/命名返回错误；
- `gcInjections=... want>=...`：固定轮数下用例未全部执行（穷举不完整）；
- `no worker progress`：watchdog 超时（GC/调用卡死）；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- 只在 clobberfree/soak 下失败：UAF/漏根；base 也失败多为调用/校验逻辑回归。

## 运行

```bash
SUITE=stress FILTER=x10 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=x10 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=x10 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 PROFILES=base ./run.sh
```

## 来源

- `docs/research/stress_extreme_survey.md` x10、§3.2/3.3/3.4（A7/A12/D20）；
- Go 运行时 `runtime/stack_test.go:657-796`（尾调用/deferreturn 栈形态）；
- CoreGoLLVM `gollvm/passes/GoFuncInfoHandler.cpp:690-791,806-838`、`GoTailcall.cpp`、`GoFixTraceback.cpp:56-115`；
- 现有 s05/n08 只做类型/解析，无 inline/musttail/deferreturn 窗口，本场景与其正交。
