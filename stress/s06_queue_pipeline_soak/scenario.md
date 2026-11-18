---
id: s06_queue_pipeline_soak
priority: P1
covers: [L02, L13, L19, L17]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

多级有界 channel 流水线长跑：`producers`（`max(2, STRESS_WORKERS/2)`）→ `in` → 2 个 stage1（内容校验、节点转换、map 指针缓存 256 条持续增长/淘汰）→ `mid` → 2 个 stage2（`interface{}` 装箱/类型断言、深拷贝、slice 缓存）→ `out` → aggregator。独立 GC churn goroutine 持续 `runtime.GC()`；stage1/stage2 排空边界各执行一次 `debug.FreeOSMemory()`。

## 不变量

1. 每条消息 head 链（4 节点）与 tail 切片（4–8 节点）的指针、值、标签、checksum 在每一级转换/深拷贝后保持不变；
2. aggregator 按 producer 校验：无丢、无重、序号集合恰为 `[0,count)`（允许跨 worker 乱序到达，但不允许缺/重）；
3. `started == finished`（每 producer），所有 producer 条数一致，stage1/stage2 的 in/out 计数与 aggregator 收包数全部相等；
4. 固定模式实际总消息数为 producer 数的整数倍（`STRESS_FIXED_ROUNDS` 向上取整），且与理论值一致；
5. stage 缓存条数不超过容量上限，随机抽样条目在 GC churn 下内容不变；
6. 优雅关闭后所有 goroutine 退出（metrics 中 `goroutines=1`），心跳持续增长，watchdog 无停滞；
7. 结束打印 `STRESS PASS s06_queue_pipeline_soak`。

## 参数

- `STRESS_SECONDS`（默认 60；0 = 固定消息数模式）
- `STRESS_FIXED_ROUNDS`（默认 512，仅 seconds=0，表示总消息数；按 producer 数向上取整）
- `STRESS_WORKERS`（默认 8；producers = max(2, workers/2)，stage1/stage2 固定各 2 个）
- `STRESS_HEARTBEAT`（默认 5s；停滞 6 个周期判失败）
- `UAF_SEED`

## 失败判读

- `phase=stage1-in/stage1-out/stage2-in/stage2-postcopy/stage2-out/aggregator ... corrupted`：跨阶段指针内容被破坏、节点标签未按阶段改写或 checksum 不符；
- `phase=stage2-box ...`：interface{} 装箱/类型断言结果错误；
- `phase=aggregator ... duplicate` / `sequence gap/out-of-range`：重消息、丢消息、序号错乱；
- `phase=final ...`：started/finished、各级计数或 producer 条数不一致；
- `phase=stage1-cache/stage2-cache ...`：map/slice 缓存中的长存活指针或值被错误回收/覆盖；
- `watchdog stalled`：死锁、channel 背压卡死或 GC 饥饿；
- 只在 clobberfree/efence 下失败：UAF。

## 运行

```bash
SUITE=stress FILTER=s06 STRESS_SECONDS=60 PROFILES=soak ./run.sh
SUITE=stress FILTER=s06 STRESS_SECONDS=3 PROFILES=soak,soak-single REPEAT=1 TIMEOUT=60 ./run.sh
SUITE=stress FILTER=s06 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=200 PROFILES=base ./run.sh
SUITE=stress FILTER=s06 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=200 PROFILES=base BUILD_FLAGS=-race ./run.sh
```
