---
id: u08_panic_stack_interact
priority: P1
covers: [L17, L04]
requires: []
default_seconds: 60
max_workers: 8
---

# 场景

深栈中的 panic 交互：每个 worker 每轮先做一次**深递归 panic 链**——`STRESS_DEPTH`（默认 320，数百层）个递归帧，每帧持有局部指针与指针槽，并在每帧注册 `defer` 闭包；栈底 panic 一个携带指针的 payload。随后 3 层嵌套 `recover` 逐层校验 payload、再 panic（新 payload 新对象），最终由最外层 recover 收口，要求恰好 3 次 recover。

同一轮再在深栈中反复触发 3 类真实 `runtime.Error` 并 recover：

- nil map 写；
- slice 越界；
- 整数除零。

这些 panic 在栈底触发，向上穿过全部递归帧的 `defer`（跨展开持指针校验），在顶层 `recover` 并校验 error 类型为 `runtime.Error`、跨展开的指针仍可读。深栈底、每层 recover 路径与轮末都强制 GC，使展开中的帧、panic payload 与 defer 闭包一起被扫描。

与 s03 的分工：s03 是"正常深递归 + 单层 panic payload"；本场景专打**展开路径**——每帧 defer、3 层 recover/再 panic 链、深栈 runtime.Error。

## 不变量

1. 每个递归帧的 `defer` 闭包在展开路径上持有的指针 canary 正确（`v == nodeMagic(seed,id)`）；
2. panic 链恰好发生 3 次 recover，且每次校验的 payload 指针有效、seed 一致；
3. 每种 runtime.Error 都被 `recover` 捕获且类型断言 `runtime.Error` 成立（非该类型的 panic 立即 FAIL）；
4. 每次深调用产生的帧 defer 数恰为 `STRESS_DEPTH+1`；整轮每 worker 为 `4×(depth+1)`，总计与 `轮数×4×(depth+1)` 精确相等（多/少都说明展开路径丢帧）；
5. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started == finished == workers`；
6. 收尾 quiesce 后连续 6 次采样 `NumGoroutine <= 基线+4`；
7. 最后一行 `STRESS PASS u08_panic_stack_interact`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤8） |
| `STRESS_DEPTH` | 320 | 递归深度（帧数），数百层级 |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | canary 种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 16 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `phase=u08.chain.frame.*` / `u08.runtime.frame.*`：展开路径上帧内指针或指针槽损坏（栈映射/展开漏标）；
- `phase=u08.chain.l1/l2/l3.payload`：payload 指针失效或 seed 不符（panic 对象在展开/GC 中被破坏）；
- `panic ... 不是 runtime.Error`：触发路径未产生预期运行时错误（逻辑错误或编译器折叠）；
- `展开帧 defer 校验=... want=...`：某帧 defer 未执行/重复执行，展开路径帧计数错误；
- `recoveries=... want=3`：嵌套 recover 链层数错误；
- `phase=watchdog`：心跳停滞，附全栈；
- `goroutine 泄漏`：收尾后未回基线；
- 只在 clobberfree/soak 下失败：展开路径指针栈映射 UAF；base 也失败多为场景逻辑错误。

## 运行

```bash
SUITE=stress FILTER=u08 STRESS_SECONDS=60 PROFILES=soak,soak-single ./run.sh
SUITE=stress FILTER=u08 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=u08 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 PROFILES=base ./run.sh
```
