---
id: s02_heap_cycle_soak
priority: P1
covers: [L05, L13, L18]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

堆循环长跑：每个 worker 反复分配指针密集 slab（`[]*node` + `[]kv` + interface + 链），保留 3 个环形 slab，周期性丢弃并 `debug.FreeOSMemory()`，再继续增长。独立 GC churn goroutine 持续 `runtime.GC()`。

## 不变量

1. 每轮新分配的 `[]*node` 全 nil、`[]kv` 全零值；
2. 环形内所有 slab 的抽样节点/键值/接口/链/sum 内容不变；
3. 收缩（FreeOSMemory）后仍保留的 slab 内容不变；
4. 心跳持续增长，watchdog 无停滞；
5. 结束打印 `STRESS PASS s02_heap_cycle_soak`。

## 参数

- `STRESS_SECONDS`（默认 60，0=固定轮数）
- `STRESS_WORKERS`（默认 4）
- `STRESS_HEARTBEAT`（默认 5s）
- `UAF_SEED`
- `STRESS_FIXED_ROUNDS`（默认 64，仅 seconds=0）

## 失败判读

- `fresh ... not zeroed`：零初始化被错误删除；
- `node/kv/interface/chain corrupted`：存活对象被回收或内容破坏；
- `watchdog stalled`：死锁/GC 饥饿；
- 只在 clobberfree/efence 下失败：UAF。

## 运行

```bash
SUITE=stress STRESS_SECONDS=60 PROFILES=soak ./run.sh
SUITE=stress STRESS_SECONDS=0 FILTER=s02 ./run.sh
```
