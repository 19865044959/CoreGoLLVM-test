---
id: l04_select_shutdown
dim: L04_scheduler_stack
priority: P2
sources:
  - grpc-go@v1.71.1: internal/transport/transport_test.go:827 GracefulClose 保留在途流、拒绝新流并检查 goroutine 收敛
  - nats-server@v2.11.0: test/cluster_test.go:124 关闭后 goroutine 数回落与优雅退出
tags: [L04, select, shutdown, worker-pool, goroutine, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认轮数，TORTURE_ROUNDS/TORTURE_JOBS 可放大）"
---

# 场景

模拟 gRPC GracefulClose 与 NATS 关闭时的 worker 池：8 个 worker 用 `select` 同时等待 `jobs` 与 `done`，主协程预填 192 个两节点任务指针（每节点带 id、值、next）。worker 每处理一个任务就把 `scratch` 指向该任务并累加校验和；主协程收齐 192 个结果后（此时 jobs 已空、worker 停在 select），显式 GC 两次，再 `close(done)`。每个 worker 退出前校验栈上 `scratch` 仍指向最后处理的完整任务，主协程校验退出计数、结果总和与全部任务对象。

# 期望行为

1. 192 个任务全部被处理，结果总和等于独立公式值；
2. 所有 worker 停在 select 时发生 GC，`scratch` 指针在关闭唤醒后仍正确；
3. 恰好 8 个 worker 通过 `<-done` 退出，无重复/遗漏退出；
4. 每个 worker 的 `lastID` 合法且对应任务对象在关闭 GC 后内容不变；
5. 关闭后再次 GC，全部 192 个任务对象仍完整；
6. 最后一行输出 `ALL PASS l04_select_shutdown`。

# 失败判读

- `worker exit check failed`：select 唤醒后 `scratch` 指针丢失或指向已回收任务；
- `job results sum`：任务处理缺失、重复或值被破坏；
- `exit count`：worker 未退出或退出两次；
- `job N corrupted after shutdown GC`：关闭路径提前回收任务对象（UAF）；
- 仅 k8s/stress 下失败：关闭与 GC 竞争下的栈扫描/调度问题。

# 参数

- `UAF_SEED`：任务值种子（默认 1）；
- `TORTURE_ROUNDS`：轮数，默认 3，范围 1–50；
- `TORTURE_WORKERS`：worker 数，默认 8，范围 1–64；
- `TORTURE_JOBS`：每轮任务数，默认 192，范围 32–4096。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_select_shutdown/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=10 TORTURE_JOBS=512 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
