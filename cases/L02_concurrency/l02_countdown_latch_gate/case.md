---
id: l02_countdown_latch_gate
dim: L02_concurrency
priority: P0
sources:
  - grpc-go@v1.71.1: test/gracefulstop_test.go:217 GracefulStop 必须阻塞到正在执行的 RPC 返回
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:141 关闭时排空残留任务、拒绝新任务
  - tailscale@v1.80.2: types/lazy/sync_test.go:127 10000 goroutine 同时首次 Get
tags: [L02, countdown-latch, one-shot, gate, broadcast, idempotent, gc, UAF_SEED]
requires: []
runtime: "<2s"
---

# 场景

模拟 grpc GracefulStop 的“等活跃工作归零再放行”与 callback serializer 的关闭闸门。6 个 worker 先报告 ready，然后阻塞在 countdown latch 上；主 goroutine 收齐全部 ready 后确认 latch 计数仍为 6、没有任何 worker 越过闸门，再执行 6 次 `CountDown` 打开 latch。打开后所有 worker 各处理 3 个固定任务。

另外验证：打开的 latch 对 5 个迟到等待者立即广播；latch 归零后的重复 `CountDown` 幂等且不会重复 close channel。

# 期望行为

1. latch 打开前 `started == 0`，计数保持 6；
2. 6 次 CountDown 后计数为 0，18 个任务全部完成且每任务恰好一次；
3. GC 在闸门关闭期间发生，payload 在放行后仍完整；
4. 迟到等待者全部被广播唤醒，重复 CountDown 不 panic、不改变状态；
5. 最后一行 `ALL PASS l02_countdown_latch_gate`。

# 失败判读

- `workers started before latch opened`：闸门未生效，worker 越过了 Wait；
- `duplicate task` / `missing task`：放行后任务分发计数错误；
- `latch count`：CountDown 记账错误；
- 超时：latch 未被扣到 0，或已打开但 Wait 未返回；
- `payload corrupted`：任务指针在 GC 后失效。

# 参数

- `UAF_SEED`：决定任务 payload 派生值，失败输出可对照该种子；
- 无 TORTURE 参数（P0 固定 6 worker × 3 任务）。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_countdown_latch_gate/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
