---
id: l02_atomic_batch_snapshot
dim: L02_concurrency
priority: P2
sources:
  - nats-server@v2.11.0: server/sublist_test.go:957 不可变快照 + atomic.Pointer 发布，读侧无锁
  - caddy@v2.10.0: admin_test.go:147 100 goroutine 并发 Load+provision 配置，读多写少的全局配置切换
  - frp@v0.67.0: pkg/config/load_test.go:59 同一配置被 N goroutine 反复解码并校验
tags: [L02, atomic.Pointer, CAS, immutable-snapshot, batch-atomicity, map-clone, generation, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 4×10 批；TORTURE_ROUNDS 放大批次，上限 16）"
---

# 场景

模拟多 publisher 对不可变 map 快照做“整批原子提交”：每批写入 4 个新 key，publisher 克隆当前快照、合并本批后 CAS 发布；CAS 失败则基于最新快照重试。读者只能看到整批出现或整批缺失，不允许半批可见。对应 nats-server sublist 的不可变快照发布与 caddy/frp 的配置热切换。

## 期望行为

1. 进程退出码为 0，最后一行 `ALL PASS l02_atomic_batch_snapshot`；
2. 最终快照代数 = 批次数（默认 40），key 数 = 批次 × 4，总和等于逐批期望值之和；
3. 读者在任意时刻观察到的快照都满足：每批 0 或 4 个 key，完整批次数 = 代数，map 长度 = 完整批次数 × 4，校验和自洽；
4. 代数单调不回退；最终快照在 GC 后保持不变；
5. 无重复 key、无缺失 key。

## 失败判读

- `reader observed invalid snapshot`：快照在发布后被继续修改，或 CAS 重试时复用了旧 map；
- `generation regressed`：发布顺序错误或使用了非原子写；
- `final keys/gen/sum`：并发提交丢批或重复提交；
- `snapshot corrupted after GC`：不可变 map 被回收/复用时被写。

## 参数

- `UAF_SEED`：决定每批 key 的数值，失败信息带种子；
- `TORTURE_ROUNDS`：放大每 publisher 批次数（默认 1，上限 16）；批次与 key 数随之线性增长，输出值确定。

## 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_atomic_batch_snapshot/*.go
UAF_SEED=1 TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
