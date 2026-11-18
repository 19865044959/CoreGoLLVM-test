---
id: s04_preempt_storm_soak
priority: P0
covers: [L03, L02]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

抢占风暴长跑：多 worker（默认 6）反复执行一段**不含函数调用**的紧循环——窗口 slice 持续轮转、从堆上读取并持有 `*cell` 指针、用 `unsafe.Pointer → uintptr → unsafe.Pointer` 往返读取字段；循环校验和由闭式公式确定。每 8 轮调用一次 `//go:noinline` helper（内含显式 `runtime.GC`），并选每 8 轮放大循环长度以跨过强抢占时间片。独立 GC churn goroutine 持续 `runtime.GC()`。可选 `STRESS_GMX=1,2,4`：主 goroutine 只在阶段边界按列表切换 `runtime.GOMAXPROCS`；未设置则不改变，尊重 `soak-single` 等 profile 的 GOMAXPROCS。

## 不变量

1. 紧循环第 k 次迭代读到 `base+((2k+1) mod 16)`，窗口首尾指针与 `cells[off]/cells[off+15]` 一致；
2. `unsafe.Pointer → uintptr → unsafe.Pointer` 往返后指针恒等、字段值不变；
3. 每轮累加校验和等于闭式期望值（`2*base*spin + 2*每块残差和 + Σ(k&3)`）；
4. 显式 GC 前后被持有的 cell 的 `pad/v` 全部不变；
5. 每 worker 的原子累加值与本地累加值一致，全局原子和等于各 worker 之和（溢出按 int64 回绕）；
6. 固定轮数模式下每 worker 恰好完成 `STRESS_FIXED_ROUNDS` 轮；心跳持续增长，watchdog 无停滞；
7. 结束打印 `STRESS PASS s04_preempt_storm_soak`。

## 参数

- `STRESS_SECONDS`（默认 60，0=固定轮数）
- `STRESS_WORKERS`（默认 6）
- `STRESS_HEARTBEAT`（默认 5s，停滞 6 个周期判 FAIL）
- `UAF_SEED`
- `STRESS_FIXED_ROUNDS`（默认 64，仅 seconds=0）
- `STRESS_GMX`（可选，如 `1,2,4`）：按列表在阶段边界切换 GOMAXPROCS；固定轮数模式按阶段均分轮数，计时模式按阶段均分时长；未设置则全程不改

## 失败判读

- `phase=tightloop ... bad=1/2/3/4`：窗口轮转错位、指针目标值被破坏、unsafe 往返指针不恒等或字段偏移读取不一致；
- `phase=checksum ... got/want`：紧循环被错误优化或持有指针/内容损坏；
- `phase=pre-gc/post-gc ... cell corrupted`：GC 扫描/回收破坏了可达对象；
- `phase=alloc ... not zeroed`：零初始化被错误删除；
- `phase=final ... rounds/atomic sum`：轮次丢失或原子累加丢失更新；
- `watchdog stalled`：STW 卡死/抢占饥饿（内层），外层超时由 runner `timeout -s QUIT` 兜底；
- 只在 clobberfree/efence 下失败：UAF；
- `asyncpreemptoff=1` 仅作控制组，勿再叠加 GOMAXPROCS=1 与超长无调用循环（可能长时间等不到安全点）。

## 运行

```bash
SUITE=stress FILTER=s04 STRESS_SECONDS=60 PROFILES=soak,soak-single,soak-noasync ./run.sh
SUITE=stress FILTER=s04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=50 PROFILES=base ./run.sh
STRESS_GMX=1,2,4 SUITE=stress FILTER=s04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=50 PROFILES=base ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=s04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=50 PROFILES=base ./run.sh
```
