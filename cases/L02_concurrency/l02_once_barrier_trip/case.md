---
id: l02_once_barrier_trip
dim: L02_concurrency
priority: P0
sources:
  - tailscale@v1.80.2: syncs/syncs_test.go:167 并发 LoadOrStore 用 start barrier 提高碰撞概率
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:105 100 个并发 TrySchedule 必须全部执行且不丢唤醒
  - etcd@v3.5.0: pkg/wait/wait_time_test.go:54 10k 个等待者单次广播全部唤醒
tags: [L02, one-shot-barrier, trip, leader-election, broadcast, memory-visibility, late-waiter, gc, UAF_SEED]
requires: []
runtime: "<2s"
---

# 场景

模拟一次性屏障（trip barrier）：8 个参与者各自写入共享 payload 后到齐，最后到达者成为唯一 leader 并广播打开；屏障此后永久打开，迟到者立即通过且不会再次 trip。对应 tailscale 测试中的 start barrier、grpc 并发回调收敛与 etcd 单次广播。

## 期望行为

1. 进程退出码为 0，最后一行 `ALL PASS l02_once_barrier_trip`；
2. 恰好 1 个 leader；8 个参与者全部通过；
3. 任意参与者通过后都能看到全部 8 份 payload（屏障的 happens-before 语义）；
4. 5 个迟到等待者全部立即返回 `late=true, leader=false`；重复 Wait 仍不阻塞、不产生新 leader；
5. GC 后 payload 不变。

## 失败判读

- `leaders != 1`：打开条件/leader 赋值有竞态，或迟到者参与了计数；
- `payloads not visible after barrier`：关闭 channel 前未完成写入，或读取侧缺少同步；
- `late waiter must not become leader`：屏障可被重复打开（不是一次性）；
- 超时：最后到达者未 close 通知 channel。

## 参数

- `UAF_SEED`：payload 种子，失败信息带种子；
- 固定 8 参与者 + 5 迟到者，无迭代放大，无墙钟阈值。

## 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_once_barrier_trip/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
