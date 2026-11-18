---
id: l03_gc_interval_lcg_loop
dim: L03_async_preempt
priority: P2
sources:
  - sonic@v1.13.2: loader/wrapper_test.go:40 TestMain 的后台 for 循环持续 runtime.GC，作为异步 GC pump 干扰被测代码
  - nats-server@v2.11.0: test/norace_test.go:285 有界轮询回调内反复 runtime.GC，观察内存上界
tags: [L03, async-preempt, gc-every-n, variable-period, lcg-schedule, in-loop-gc, safepoint, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

主循环每轮解引用 512 槽环上的 `*glNode`，在无函数调用的 8 次迭代块中累加；轮次命中 LCG 计划出的 GC 点时，调用 noinline 的 `glTick` 分配垃圾并执行 `runtime.GC()`，返回后继续解引用同一节点。GC 间隔在 8..200 轮之间按种子变化，不是固定 N。结束前用独立的 LCG 模拟复算 GC 次数与每次 gcTick 的垃圾校验和，并逐节点复核环形 backing。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l03_gc_interval_lcg_loop`；
2. 实际 GC 次数与独立 LCG 计划完全一致；
3. 循环累加值与各次 gcTick 返回值的总和等于期望和；
4. 每次循环内 GC 后原 `*glNode` 指针仍可读，tag/字段不变；
5. 结束后 512 个环节点全部完整；
6. 在 clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `FAIL: variable-period GC count`：GC 点调度错误或循环被优化掉；
- `FAIL: GC tick and loop checksum`：循环体/GC 调用点执行偏差，或节点值被破坏；
- `FAIL: node readable after in-loop GC`：跨 GC 的局部指针未被正确扫描；
- `SIGSEGV` / `fatal error: found bad pointer`：疑似栈映射问题；
- 超时：GC 次数异常放大（检查 TORTURE_ROUNDS 与 LCG 计划）。

## 参数

- `UAF_SEED`：决定环访问步长/偏移与 LCG 初始状态；失败信息带出种子；
- `TORTURE_ROUNDS`：放大默认 32768 轮（上限 1<<20），GC 间隔分布不变。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_gc_interval_lcg_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=262144 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
