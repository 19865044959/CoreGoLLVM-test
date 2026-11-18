---
id: l03_call_each_round_stack
dim: L03_async_preempt
priority: P2
sources:
  - etcd@v3.5.0: pkg/adt/interval_tree_test.go:359 随机区间插入/Stab/删除 + 递归 Visit，制造栈增长
  - nats-server@v2.11.0: test/service_latency_test.go:1226 递归解析导入链（原栈溢出回归用例）
tags: [L03, async-preempt, call-per-round, stack-growth, stack-shrink, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

长循环每轮调用一次 `//go:noinline` 的深度 20 递归链，每层声明 `[256]int64`（2KiB）栈帧并沿链传指针，单次调用约 42KiB 栈，迫使 goroutine 栈反复增长/复制。每 8 轮在浅栈上分配垃圾并显式 `runtime.GC()`，触发栈收缩；GC pump 并发执行 8 轮 GC，可能在递归中途打断调用链。默认 1024 轮，`TORTURE_ROUNDS` 可放大。

# 期望行为

1. 并发 GC pump 至少完成一轮；
2. 默认 1024 轮（或 `TORTURE_ROUNDS` 指定轮数）全部执行；
3. 累加和等于递归返回值闭式公式 `(depth+1)×base` 的逐轮和；
4. GC 后全部节点字段不变；
5. 进程退出码为 0，最后一行 `ALL PASS l03_call_each_round_stack`；
6. 各 profile 下结果一致。

# 失败判读

- `FAIL: GC pump completed during call-per-round loop`：深度调用期间 GC 未完成；
- `FAIL: call-per-round stack checksum (seed=...)`：递归栈帧中的值或指针被破坏；
- `FAIL: call-chain nodes intact after GC (seed=...)`：跨栈增长持有的节点被回收；
- 崩溃/超时：栈复制、栈扫描或抢占异常。

# 参数

- `UAF_SEED`：节点访问步长、偏移与递归 salt，默认 1，失败输出带出；
- `TORTURE_ROUNDS`：循环轮数（默认 1024）；每轮约 42KiB 栈写入，放大时线性增长。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_call_each_round_stack/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=16384 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
