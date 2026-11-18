---
id: l04_worker_pool_cancel_drain
dim: L04_scheduler_stack
priority: P1
sources:
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:58 并发调度的回调在 ctx.Done() 上退出，覆盖池内任务级取消
  - go1.24.1: src/context/context.go:240 WithCancel 建立父子取消注册，池 ctx 取消时向全部任务传播
tags: [L04, worker-pool, context-cancel, select, goroutine, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 gRPC callback serializer 的固定 worker 池与 NATS 式池级关闭叠加逐任务取消：4 个 worker 从 `jobs` 取任务，任务在自己的检查点上 `select` 等待 `release` 或 task ctx。Phase A 共 16 个任务，其中 4 个在提交前被逐任务取消；其余 12 个必须在检查点等待 release。首批任务阻塞且各持 3 节点指针链时显式 GC，放行后取消者只能以 canceled 结束。Phase B 用池 ctx 派生全部任务 ctx，4 个任务开跑后 GC 并取消整个池，worker 从 `jobs`/`stop` 的 select 退出，运行中与排队中的任务都必须观察到取消且无一完成。

# 期望行为

1. Phase A 恰好 12 个任务 done、4 个任务 canceled，无任务既未运行也未取消；
2. 取消任务不会从 release 醒来，也不会写入完成状态；
3. Phase B 池取消后所有任务（运行中或仍在队列）的 `ctx.Err()==context.Canceled`，完成数为 0；
4. 池 worker 全部退出，无 goroutine 泄漏导致的挂起；
5. 两个 phase 的 GC 后，所有任务保留的 3 节点链指针身份与值不变；
6. 最后一行输出 `ALL PASS l04_worker_pool_cancel_drain`。

# 失败判读

- canceled 任务变成 done：检查点 select 在 ctx 已取消时错误选择了 release（或取消传播丢失）；
- Phase B 有任务 done：池 ctx 未传播到子 ctx，或 worker 在 stop 后仍执行任务；
- 任务状态 pending 且 ctx 未取消：池级取消未覆盖排队任务；
- `chain corrupted`：任务阻塞期间 GC 未保留栈/channel 中的指针；
- 超时：worker 未从 jobs/stop select 退出，或 release 未放行。

# 参数

- `UAF_SEED`：链基值与任务值扰动（默认 1）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_worker_pool_cancel_drain/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=7 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
