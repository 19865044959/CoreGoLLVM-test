---
id: l04_waitgroup_wave_barrier
dim: L04_scheduler_stack
priority: P1
sources:
  - grpc-go@v1.71.1: test/gracefulstop_test.go:217 GracefulStop 必须等全部在途工作完成（WaitGroup 屏障语义）
  - pion/webrtc@v4.1.2: operations_test.go:13 16 个 operation × 100 轮依次推进并在每轮汇合
tags: [L04, WaitGroup, wave, barrier, goroutine, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

5 个波次，每波用全新的 `sync.WaitGroup` 拉起 8 个 worker；worker 在波内屏障处阻塞（各自链活跃在栈上）时 GC，`Wait` 返回后才进入下一波。共享波次标记与 active 计数用于检测“上一波未结束就开始下一波”。

# 期望行为

1. 每波 8 个 worker 各执行一次，结果按 (wave,id) 唯一且与期望和一致；
2. `Wait` 返回时 active 计数归零；下一波启动时上一波不得有任何 worker 存活；
3. 所有 worker 观测到的共享波次标记始终等于自己所属波次（无跨波串扰）；
4. 波内屏障处 GC 后，worker 的链与结果仍正确；波间 GC 后已保留结果链完整；
5. active 峰值恰为每波 worker 数；
6. 最后一行输出 `ALL PASS l04_waitgroup_wave_barrier`。

# 失败判读

- 某波结果缺失/重复：WaitGroup 计数错误导致 Wait 提前返回；
- wave marker 不匹配：波次重叠，屏障语义破坏；
- active 归零断言失败：有 worker 越过 Wait 仍在运行；
- “chain corrupted”：GC 与 fan-in 交接之间的指针保留问题；
- 超时：worker 未放行或 Wait 永不返回。

# 参数

- `UAF_SEED`：链基值与波次扰动（默认 1）；
- `TORTURE_ROUNDS`：波数（默认 5，范围 1..16）；
- `TORTURE_WORKERS`：每波 worker 数（默认 8，范围 2..24）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_waitgroup_wave_barrier/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=6 TORTURE_ROUNDS=12 TORTURE_WORKERS=16 GOGC=1 GOMAXPROCS=2 /tmp/case
```
