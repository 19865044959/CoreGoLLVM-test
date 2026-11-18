---
id: l05_goroutine_churn_alloc
dim: L05_alloc_gc
priority: P2
sources:
  - sonic@v1.13.2: decoder/decoder_test.go:47 的 TestGC：1 万 goroutine 解码后各自 runtime.GC
  - pgx@v5.7.4: pgconn/pgconn_stress_test.go:16 的 TestConnStress：10000 次动作后 goroutine 数受限
  - badger@v4.5.0: db_test.go:1905 的 TestGoroutineLeak：订阅/取消后 NumGoroutine 回落
tags: [L05, goroutine-churn, alloc-churn, concurrency, liveness, gc, torture]
requires: []
runtime: "<10s（默认 2 波）"
---

# 场景

每波启动 48 个短命 goroutine，每个分配 192 个小对象（含 string、[]byte、指针
字段）并计算确定性 sum/FNV 摘要后退出；后台 churn goroutine 持续分配 16KB
缓冲制造垃圾，波间显式 GC。主 goroutine 按 worker 索引重算期望值并校验，
最后确认所有 goroutine 退出、结果 pin 跨 GC 不变。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_goroutine_churn_alloc`；
2. 每波 48 个 worker 各返回一份结果，无重复、无缺失；
3. 每份结果的 sum/hash/n 与主 goroutine 独立重算的期望完全一致（与调度顺序无关）；
4. 每波结束显式 GC 后，pin 的结果仍与期望一致；
5. 关闭 churn goroutine 并 GC 后，`NumGoroutine` 回落到基线附近（容差 8）；
6. 在 base/clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `duplicate/missing worker` 或 `sum/hash` 不符：goroutine 间数据串扰或对象复用错误；
- `goroutine churn leak`：短命 goroutine 或 churn goroutine 未退出；
- 崩溃/`found bad pointer`：栈或堆上的小对象图在 churn 中被错误回收。

## 参数

- `UAF_SEED`：决定每个小对象的值与哈希；失败输出带种子；
- `TORTURE_ROUNDS`：波数，默认 2，范围 1–8；放大后 goroutine 与分配总量线性增长。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_goroutine_churn_alloc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_ROUNDS=8 /tmp/case
```
