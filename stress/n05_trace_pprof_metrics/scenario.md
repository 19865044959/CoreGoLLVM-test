---
id: n05_trace_pprof_metrics
priority: P1
covers: [L04, L05, L14]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

可观测性长跑：多 worker 在 `pprof.Do` label 内执行深递归（每层指针槽 + 局部指针），穿插 `chan *node` 交换与 256 节点/32 项 map 分配；独立 GC churn goroutine 持续 `runtime.GC()`。观察者 goroutine 串行反复 `runtime/trace.Start/Stop`（`io.Discard` 优先，失败依次退 `/dev/null`、内存 buffer），每轮写 goroutine/heap/block/mutex profile（`WriteTo(buf, 1)`，校验文本头部），并周期 `runtime/metrics.Read` 解析样本值。

## 不变量

1. 每层递归展开时 `slots[0]/slots[7]/local` 与传入指针关系不变，顶层 checksum 与公式一致；
2. channel 传输的 `*node` 内容不变，分配后的 slice/map 内容不变；
3. 每次 `trace.Stop` 后（校验轮）输出以 `go 1.` 开头且含 `" trace\x00\x00\x00"` 版本标记；
4. goroutine profile 以 `goroutine profile: total ` 开头，heap 以 `heap profile: ` 开头，block/mutex 分别以 `--- contention:` / `--- mutex:` 开头；
5. `/sched/goroutines:goroutines`、`/memory/classes/heap/objects:bytes`、`/gc/cycles/total:gc-cycles` 均存在且 kind 为 uint64，goroutines 样本非 0，gc cycles 不倒退；
6. 心跳持续增长，watchdog 无停滞；
7. quiesce 后 goroutine 数不超过启动基线 + 8（观察者/GC/watchdog 无泄漏）；
8. 结束打印 `STRESS PASS n05_trace_pprof_metrics`。

## 参数

- `STRESS_SECONDS`（默认 60，0=固定轮数）
- `STRESS_WORKERS`（默认 4，上限 16）
- `STRESS_DEPTH`（默认 96，范围 16–512）
- `STRESS_TRACE_MS`（默认 8ms，每轮 trace 采集窗口）
- `STRESS_HEARTBEAT`（默认 5s）
- `STRESS_FIXED_ROUNDS`（默认 16，仅 seconds=0）
- `UAF_SEED`

## 失败判读

- `phase=trace: Start failed`：trace 运行时不可用或并发 Start（本场景已串行化）；
- `phase=trace: header ... corrupted`：trace 头部/版本标记缺失，trace buffer 或写出路径异常；
- `phase=pprof: ... header corrupted` / `WriteTo`：profile 生成或栈回溯路径异常；
- `phase=metrics: ... not registered/kind`：metrics 注册表或样本解析异常；
- `phase=deep/chan/alloc ... corrupted`：栈帧/指针内容被破坏（GC 漏标或栈扫描问题）；
- `watchdog stalled`：SIGPROF/GC/trace 路径死锁或调度饥饿；
- `goroutine baseline exceeded`：观察者/trace 内部 goroutine 泄漏。

## 运行

```bash
SUITE=stress STRESS_SECONDS=60 PROFILES=soak ./run.sh
SUITE=stress STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 FILTER=n05 ./run.sh
SUITE=stress FILTER=n05 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 BUILD_FLAGS=-race ./run.sh
```

## 来源

- `go/src/runtime/trace/flightrecorder_test.go:66-124`（trace Start/Stop + 快照并发）
- `go/src/runtime/pprof/pprof_test.go:1621-1700,1840-1900,2253-2346`（goroutine/block/mutex profile 并发写）
- `go/src/runtime/metrics_test.go:211-437`（metrics 键与样本解析）
- `go/src/runtime/goroutineleakprofile_test.go:16-70`（goroutine 基线/泄漏判定）
