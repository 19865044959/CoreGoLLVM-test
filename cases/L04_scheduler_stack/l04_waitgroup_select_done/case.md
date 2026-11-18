---
id: l04_waitgroup_select_done
dim: L04_scheduler_stack
priority: P0
sources:
  - etcd@v3.5.0: server/mvcc/kvstore_test.go:783 goroutine 内 wg.Wait() 后 close(doneC)，:788 主协程 select doneC 与超时
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:127 同样的 wg.Wait()+close(done)+select 完成观测模式
tags: [L04, WaitGroup, select, done-channel, cancel, goroutine, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 gRPC 回调汇合与 etcd 后台任务收尾中“WaitGroup 完成信号经 channel 交给 select”的模式。三个 phase 共用 24 个 worker：

1. Phase A：worker 在 start 上阻塞时 GC，放行后各发 16 个进度事件，主协程在 select 中同时收 `progress` 与 `wg.Wait→close(done)`；断言事件总数 24×16、每 worker 序号不重不漏；
2. Phase B：halt 在 worker 启动前关闭，worker 走 select 取消分支，done 关闭且进度事件为 0；
3. Phase C：每个 worker 恰好发 1 个事件后阻塞在 halt 上，GC 后放行；done 关闭，事件总数恰为 24，无多余事件。

# 期望行为

1. Phase A 开始前 done 未关闭；完成后 done 关闭且收到 24×16 个唯一 `(id,seq)` 事件；
2. Phase A done 后无残留进度事件，所有 worker 保留链完整；
3. Phase B 所有 worker 走取消分支，progress 为 0，done 关闭；
4. Phase C 在 worker 阻塞于 halt 时 done 不关闭，放行后 done 关闭且事件恰为 24；
5. 三个 phase 的 GC 点均不破坏 worker 栈上的 3 节点链；
6. 最后一行输出 `ALL PASS l04_waitgroup_select_done`。

# 失败判读

- `allDone closed before workers started`：WaitGroup Add 与 Wait 的顺序错误（早期关闭）；
- 事件重复/缺失：select 分支丢事件或 WaitGroup 计数错误导致 done 提前/延后；
- Phase B/C 计数不符：取消分支被错误选择或 worker 多跑一轮；
- `chain corrupted`：GC 与 fan-in 交接之间的指针保留问题；
- 超时：done 永不关闭或进度通道容量/消费不平衡。

# 参数

- `UAF_SEED`：链基值与每 worker 步长（默认 1）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_waitgroup_select_done/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=17 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
