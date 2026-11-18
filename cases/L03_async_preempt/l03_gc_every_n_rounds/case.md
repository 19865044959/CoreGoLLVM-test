---
id: l03_gc_every_n_rounds
dim: L03_async_preempt
priority: P2
sources:
  - quic-go@v0.50.1: frame_sorter_test.go:1377 1000 个帧乱序 Push，每 N 次 Push 强制 GC，最终按 offset 还原数据流
  - nats-server@v2.11.0: server/norace_test.go:8101 5 账号 × 长循环投递，GC 每 10k 次，校验消息总量
tags: [L03, async-preempt, gc-every-n, round-counter, tight-loop, safe-point, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

主 goroutine 在默认 1048576 轮的无调用持针循环中累加 2048 槽位环上的值，并把轮次写入原子计数；控制 goroutine 观察该计数，每当累计到 N=16384 的整数倍就分配垃圾并 `runtime.GC()`，恰好完成 `轮数/N` 次后退出。主循环的循环体只有整数运算、原子写和指针解引用，GC 只能依靠循环回边安全点异步抢占落到任意两轮之间。默认总轮数与 GC 次数固定，`TORTURE_ROUNDS` 可放大总轮数（每次 GC 间隔不变）。

# 期望行为

1. 进程退出码为 0；
2. 最后一行输出 `ALL PASS l03_gc_every_n_rounds`；
3. GC 次数恰等于 `目标轮数 / 16384`，即严格每 N 轮一次；
4. 主循环至少完成目标轮数，且未超出安全上限；
5. 累计校验和与按实际轮数重放的结果一致；
6. 2048 个槽位在全部 GC 后保持原值；
7. 在 base/clobberfree/efence/k8s 等 profile 下输出与校验结果一致。

## 失败判读

- `FAIL: GC fired exactly every N rounds`：控制 goroutine 的计数观察或退出条件错误（用例/环境问题），也可能是调度长期饥饿；
- `FAIL: loop reached target rounds`：主循环未跑到目标，检查原子计数或超时；
- `FAIL: gc-every-n checksum after GC`：持针循环中的环数据被 GC 破坏；
- `FAIL: ring intact after ... GCs`：槽位被写坏或指针误用；
- 超时：控制 goroutine 等待轮次计数时死循环。

## 参数

- `UAF_SEED`：决定环访问步长与偏移；失败信息带出种子；
- `TORTURE_ROUNDS`：替换默认 1048576 轮，用于放大总轮数与 GC 次数；每 16384 轮一次 GC 的间隔不变。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_gc_every_n_rounds/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=4194304 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
