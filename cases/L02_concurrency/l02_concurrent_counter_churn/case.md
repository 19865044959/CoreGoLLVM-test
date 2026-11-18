---
id: l02_concurrent_counter_churn
dim: L02_concurrency
priority: P2
sources:
  - frp@v0.67.0: pkg/util/metric/counter_test.go:9 原子计数 + 快照/清零
  - tailscale@v1.80.2: syncs/shardvalue_test.go:16 1000×10000 原子分片计数 + 长循环
  - caddy@v2.10.0: modules/caddyhttp/metrics_test.go:30 in-flight gauge 归零
tags: [L02, atomic, counter, CAS-max, concurrent-alloc, interior-pointer, gc, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 3200 次自增；TORTURE_ROUNDS 放大迭代数）"
---

# 场景

模拟 frp metric counter、tailscale shardvalue 与 caddy in-flight gauge 的并发计数：8 个 worker 各做 400 次 `atomic.Int64.Add`，并用 CAS 循环维护全局峰值；每 32 次迭代分配一个 16 元素的 `[]node` 与指针切片（指针指向数组内部元素），`runtime.GC()` 后逐个校验内部指针与字段，再释放引用。每个 worker 另维护本地计数，结束后主 goroutine 校验共享总数、CAS 峰值、各 worker 本地计数之和与活跃块计数四个不变量。

# 期望行为

1. 共享计数 `total == 8*400 == 3200`，CAS 峰值 `max == total`；
2. 每个 worker 本地计数均为 400，求和等于共享总数（不丢不重）；
3. 指向数组内部的 `*node` 在 GC 后字段不变；
4. 活跃块计数最终归零，最终 GC 后共享计数与峰值不变；
5. 默认输出 `workers=8 increments=3200 max=3200`，最后一行 `ALL PASS l02_concurrent_counter_churn`。

# 失败判读

- `total counter` / `sum of per-worker counters`：atomic 自增丢失或本地计数串扰；
- `max counter`：CAS 峰值维护错误（观察到小于总数说明 CAS 路径漏更新）；
- `interior pointer corrupted after GC`：指向数组内部的指针（含栈上切片头/堆数组）在 GC 后失效；
- `live chunk counter did not return to zero`：GC 后引用计数/生命周期管理错误。

# 参数

- `TORTURE_ROUNDS`：迭代数倍数，默认 1（400 次/worker），范围 1..100；
- `UAF_SEED`：由 runner 注入但本用例不使用。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_concurrent_counter_churn/*.go
GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=10 GODEBUG=clobberfree=1 /tmp/case
```
