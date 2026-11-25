---
id: u07_goroutine_spawn_churn
priority: P0
covers: [L04, L17]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

goroutine 生成/退出翻腾：每 worker 每轮 spawn 192 个**短 goroutine**（数百量级），每个 goroutine 申请 canary 对象（`v == nodeMagic(seed,id)`，探针与 u01 完全一致），在退出窗口校验后销毁。按退出形态分三类：

- `defer`（1/3）：`defer` 闭包持指针，函数返回（退出路径）时逐槽校验并把指针交回父 goroutine；
- `panic`（1/3）：`panic(指针)`，`recover` 后校验 payload 指针与帧内指针槽，再交回；
- `Goexit`（1/3）：`runtime.Goexit()` 前把指针交回 channel，整条退出调用链上的 `defer` 继续持指针校验。

主循环在 spawn 中途（一半已启动）与轮末各强制 `runtime.GC()`，另有 GC churn goroutine 每 5ms 持续 GC；父 goroutine 每轮收齐 192 个交回的指针，逐槽复核 canary、id 唯一性与退出形态。这样退出中的 goroutine 栈、即将进入 stackpool 的栈缓存会被反复扫描/复用。

与其它场景的分工：s01 的 goroutine-per-request 只覆盖正常请求生命周期；u02 打抢占瞬间寄存器/未 spill 槽的扫描；本场景专打**退出窗口与栈缓存复用窗口**。与 s03 的差异：s03 打栈增长/展开时的帧内指针校验，本场景的对象在父子 goroutine 间通过 channel 交接，重点在 goroutine 退出瞬间与 stackpool 栈缓存被新 goroutine 复用时的扫描。

## 不变量

1. 每个 canary 对象在 goroutine 内（body/退出路径）与父 goroutine 收到后（after-exit）两段校验都满足 `v == nodeMagic(seed,id)`，任何失配立即 FAIL；
2. 每轮父 goroutine 恰好收到 `perRound=192` 个指针，id 落在本轮区间且两两不重复，退出形态与提交位匹配；
3. `started == finished == perRound × 总轮数`（有 goroutine 卡在退出路径即失败）；
4. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；
5. 收尾 quiesce（停止 churn/watchdog）后连续 6 次采样 `NumGoroutine <= 基线+4`；
6. 最后一行 `STRESS PASS u07_goroutine_spawn_churn`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | canary 种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 16 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

每轮每 worker 的 spawn 数为编译期常量 `perRound=192`。

## 失败判读

- `phase=... canary v=... want=...`：对象内容损坏（UAF/漏扫描）——重点看 `phase=u07.defer.exit|u07.panic.recover|u07.goexit.defer|u07.parent.after-exit`，其中 `parent.after-exit` 失配说明对象在 goroutine 退出/栈缓存复用后被回收；
- `phase=... nil *node`：指针被清空或对象被回收；
- `mode=... want=...`：交回的指针不属于该退出形态（逻辑错误）；
- `重复 id` / `id=... 越界`：goroutine 交回重复或错误对象；
- `started=... finished=...`：有 goroutine 未走完退出路径（死锁/Goexit 卡住）；
- `phase=watchdog`：心跳停滞，附全栈；
- `goroutine 泄漏`：收尾后 NumGoroutine 未回基线；
- 只在 clobberfree/soak 下失败：典型的栈扫描/栈缓存复用 UAF；base 也失败多为场景逻辑或探针错误。

## 运行

```bash
SUITE=stress FILTER=u07 STRESS_SECONDS=60 PROFILES=soak,soak-single ./run.sh
SUITE=stress FILTER=u07 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=u07 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 PROFILES=base ./run.sh
```
