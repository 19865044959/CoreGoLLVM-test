---
id: l02_semaphore_ctx_panic
dim: L02_concurrency
priority: P1
sources:
  - tailscale@v1.80.2: syncs/syncs_test.go:145 容量 2 的 Semaphore：Acquire/TryAcquire/Release，已取消 ctx 的 Acquire 立即失败
  - gorm@v1.30.0: tests/prepared_stmt_test.go:153 MaxOpenConns(1) 下 100 goroutine 并发 Create/First，单连接信号量不得泄漏
  - quic-go@v0.50.1: streams_map_outgoing_test.go:456 并发等待者被 context 取消后从等待集合移除
tags: [L02, semaphore, context-cancel, panic-recover, deferred-release, token-conservation, peak, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟资源信号量的取消与 panic 安全释放：容量固定的 channel 信号量，等待者可能被 context 取消；持有者可能 panic，必须由 defer 先归还令牌再 recover。覆盖 tailscale Semaphore 的取消路径、gorm 单连接池争用和 quic-go 等待者清理。

## 期望行为

1. 进程退出码为 0，最后一行 `ALL PASS l02_semaphore_ctx_panic`；
2. 占满时 TryAcquire 失败、已取消 ctx 的 Acquire 立即 `context.Canceled`，峰值恰为容量 4；
3. 释放 1 个令牌后恰有 1 个等待者持有，其余 2 个取消；令牌最终归零；
4. 4 个持有者 panic，全部 recover 且令牌归还；
5. 8 worker × 30 次 churn（每 7 次 panic 一次）后：共 36 次 panic 被 recover，`acquired == released`、in-flight=0、峰值 ≤ 容量；
6. GC 后无令牌泄漏。

## 失败判读

- `token leaked on panic path`：defer 中 release 与 recover 顺序错误，或 panic 跳过了归还；
- `no waiter acquired the released token` / `accepted != 1`：释放唤醒逻辑或取消竞态处理错误；
- `acquire/release imbalance`：成功 Acquire 后未在所有路径 Release；
- `peak exceeds capacity`：信号量容量计数错误。

## 参数

- `UAF_SEED`：载荷/失败种子（本用例主要用于失败信息）；
- 固定容量 4、等待者 3、churn 8×30；panic 次数由 `i%7==3` 确定性给出，无墙钟阈值。

## 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_semaphore_ctx_panic/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
