---
id: l03_select_nested_dispatch
dim: L03_async_preempt
priority: P1
sources:
  - etcd@v3.5.0: raft/node_test.go:369 goroutine 内 for+select 接收 stop/ticker/Ready，Ready 分支内继续处理事件
  - nats-server@v2.11.0: server/norace_test.go:10269 select 同时监听 ticker、client channel 与 timeout，循环排空
tags: [L03, async-preempt, nested-select, select-default, buffered-channel, worker-loop, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

三层嵌套 select 的事件分发循环：外层 select 在 `jobCh` 与 `quit` 之间选择；收到任务后，内层 `select` 非阻塞消费 `urgentCh`（无紧急项时走 `default`）；进入 urgent 分支后再用第三层 select 在 `laneA`/`laneB` 之间选择。生产者按 `UAF_SEED` 决定哪些轮次发送 urgent 以及车道，并预先向 cap=1 的缓冲 channel 填入唯一可用消息，保证每层 select 只有一个 case 就绪、分支选择确定。worker 每 64 个任务在仍持有 task/urgent/lane 三枚节点指针时显式 `runtime.GC()`，GC 后复核 tag 再计算应答；另有并发 GC pump。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l03_select_nested_dispatch`；
2. 全部 512 个任务的应答值与车道名匹配生产者的期望；
3. urgent/default 计数、第三层 laneA/laneB 计数与生产者计划完全一致；
4. worker 显式 GC 次数为 8，GC 后三枚节点均可安全读取；
5. 应答值总和等于期望和，GC pump 至少完成 1 轮；
6. 在 clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `FAIL: nested select ack values/lane`：嵌套 select 选错分支或消息指针损坏；
- `FAIL: nodes held across worker GC`：worker 栈上的节点指针未被正确扫描；
- `FAIL: inner select` / `third-level lane split`：预填充协议或 select 语义异常；
- 超时：select 死锁，检查 job/urgent/lane 的握手顺序。

## 参数

- `UAF_SEED`：决定 urgent 轮次与车道分配；失败信息带出种子；
- 其他 TORTURE_* 参数：本用例不消费。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_select_nested_dispatch/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
