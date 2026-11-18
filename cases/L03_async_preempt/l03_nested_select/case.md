---
id: l03_nested_select
dim: L03_async_preempt
priority: P1
sources:
  - etcd@v3.5.0: raft/node_test.go:353 node.run 单 goroutine select{tick, prop, recv, ready, stop} 事件循环
  - grpc-go@v1.71.1: test/context_canceled_test.go:126 取消与压缩标志经历两次 select 竞争，不能产生不一致头部
tags: [L03, async-preempt, nested-select, select-default, worker-loop, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟事件循环 worker：外层 `select` 在普通任务队列 `jobCh` 与 `stop` 之间选择；每取到一个任务，先用内层 `select { case <-urgentCh: ... default: }` 非阻塞排空紧急队列，再处理任务。任务处理内部对任务节点指针做 16 次无调用紧循环。主 goroutine 按 `UAF_SEED` 排列发送 1024 个任务，并穿插发送 256 个紧急项；worker 每处理 64 个任务显式 `runtime.GC()`，另有 GC pump 并发执行 8 轮 GC。收到关闭的 `jobCh` 后 worker 再排空一次紧急队列，保证计数确定。

# 期望行为

1. 进程退出码为 0；
2. 最后一行输出 `ALL PASS l03_nested_select`；
3. worker 处理全部 1024 个任务，且没有重复任务或重复紧急项；
4. 排空全部 256 个紧急项；
5. 任务（每项重复 16 次）与紧急项的累加校验和等于预构造总和；
6. worker 每 64 个任务显式 GC 一次（共 16 次），并发 GC pump 至少完成一轮；
7. 全部任务/紧急节点在 GC 后保持原值；
8. 在 base/clobberfree/efence/k8s 等 profile 下输出与校验结果一致。

## 失败判读

- `FAIL: worker processed every job` / `missing job`：嵌套 select 退出或关闭顺序导致任务丢失；
- `FAIL: worker drained every urgent item`：内层 select 的 default 退出过早或排空不完整；
- `FAIL: no duplicate jobs or urgent items`：内层/外层队列重复消费；
- `FAIL: nested select checksum after GC`：任务指针被 GC 破坏；
- 超时：内层 select 阻塞或 close 顺序造成死锁。

## 参数

- `UAF_SEED`：任务发送排列由该种子派生；失败信息带出种子；
- 无其他放大参数，P1 固定规模。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_nested_select/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
