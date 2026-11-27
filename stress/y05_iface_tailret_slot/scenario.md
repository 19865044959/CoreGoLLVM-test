---
id: y05_iface_tailret_slot
priority: P0
covers: [L17, L06]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

接口/尾调用 × 返回槽（D20 GoTailCall × L06 itab 派发）。g01 只打「命名结果 + 堆逃逸闭包晚写」这一种返回槽形状；本场景把同一个未初始化返回槽窗口压到 itab 派发 / wrapper / 尾调用路径上：

- **接口方法返回大值**：`getter interface{ Get() Big }` 多实现——值 receiver（`Big` 自身 264B 按值拷贝、`bigImpl` 296B 复合）与指针 receiver（`ptrImpl`）；`g.Get()` 经 itab 派发（wrapper 复制 receiver 后可能尾调用真实方法），callee 内**写前 GC** 扫描调用方仍未写入的返回槽；
- **大值 receiver 拷贝 × 方法值**：`mv := bv.Get` 存变量反复调用（值 receiver 方法值闭包按值捕获 264B receiver）；`victimMval` 写前/写后 GC；
- **每次调用后 noinline GC helper**：`consumeBig` 在 GC 前后各校验一遍返回值全部指针 canary + words + seq（按各实现变换的期望值）；
- **脏栈铺垫**：`dirtyRec` 以深度 d=3+r%96 递归把各层帧刷满指向短命堆对象的指针，depth 0 处 GC 清扫成悬挂；`ifaceRec/mvalRec/chainA-B` 同型同深度下降，未初始化返回槽落在旧 dirtyRec 帧的脏字上；
- **尾调用链**：`chainA→chainB→…→chainLeaf` 每层尾位置调用，链中（`chainB` 抽样层 + `chainLeaf`）`runtime.GC()` 在「上层返回槽未写」窗口扫栈，大值层层穿透；
- **weak 强引用断言**：链返回值仍由本地强引用时 GC，`weak.Value()` 必须非 nil。

差异说明：与 g01 不同，晚写入者不是堆逃逸闭包，而是 itab wrapper / 方法值闭包 / 尾调用链——直接打 D20 尾调用与 wrapper 栈图。返回值变换（seq/words[0]/words[15]）按实现种类区分，逐字段核对。官方 Go 恒 PASS；崩溃/漏标仅随 gollvm 出现。

来源：`docs/research/stress_batch4_design.md` y05；g01 脏栈/poison 手法；`gp/GoTailcall.cpp`、itab wrapper 生成路径；`cases/L06_*`（iface 派发矩阵）。

## 不变量

1. 三种实现（Big 值 receiver / bigImpl / ptrImpl）经 itab 派发的返回值在 `victimIface` 写后 GC、`consumeBig` 前后 GC 中节点指针/words/seq 全部与期望一致；
2. 方法值（值/指针 receiver）每次调用返回值同样逐字段一致（值 receiver 闭包捕获 264B 按值拷贝）；
3. 尾调用链 a→b→leaf 层层穿透后返回值完整（nodeBase=leafBase，words 为标准常量）；
4. 每轮 `iface_calls==1`、`method_calls==5`（1 下降 + 3 反复 + 1 指针）、`chain_calls==1`；weak 探针每轮 1 次且非 nil；
5. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；worker `started==finished`；
6. 收尾 quiesce 后 goroutine 回基线（+4 容差）；`Big` 尺寸恒 264B（preflight 断言）；
7. 最后一行 `STRESS PASS y05_iface_tailret_slot`。

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

- `canary ... (UAF/帧扫描漏标)` / `nil *node`：wrapper/尾调用路径返回槽漏标，节点被提前回收——最敏感于 `window`（gcstoptheworld=2）；
- `words/seq ... (返回值被改写)`：返回槽与脏字节交错（栈图区间错误 / 尾调用帧复用错误）；
- `id=%d want=%d (返回值被复用)`：返回槽落在复用 span 的活对象上（poison 填埋命中）；
- `strong ref held but collected`：写后指针未被标记（栈图漏标）；
- `iface_calls/method_calls/chain_calls ... want`：派发路径被静默跳过（编译器优化异常）；
- `6 个心跳周期无进展`：卡死（附全栈）；`goroutines baseline ... leak`：收尾泄漏；
- 只在 `detector`（efence）崩溃：释放对象被再次访问（UAF）。

## 运行

```bash
SUITE=stress FILTER=y05 STRESS_SECONDS=3 PROFILES=soak,window REPEAT=1 TIMEOUT=120 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=y05 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base,clobberfree REPEAT=1 TIMEOUT=180 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=y05 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 BUILD_FLAGS=-race PROFILES=base REPEAT=1 TIMEOUT=300 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
```

## 来源

- `docs/research/stress_batch4_design.md` y05（D20 尾调用 × 返回槽组合）；
- `stress/g01_goret_dirty_slot`（脏栈/poison/同构下降手法）、`stress/x05_goexit_panicframe`（Big 264B 形状）；
- `gp/GoTailcall.cpp`、itab wrapper 生成；`cases/L06_reflect_iface/*`（派发矩阵）。
