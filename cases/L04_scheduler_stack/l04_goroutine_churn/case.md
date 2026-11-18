---
id: l04_goroutine_churn
dim: L04_scheduler_stack
priority: P1
sources:
  - grpc-go@v1.71.1: internal/leakcheck/leakcheck_test.go:41 goroutine 创建/退出计数与生命周期跟踪
  - pion/webrtc@v4.1.2: operations_test.go:13 16 个 operation × 100 轮 goroutine 调度与 FIFO 汇合
tags: [L04, GPM, goroutine, waitgroup, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s"
---

# 场景

模拟 gRPC/WebRTC 中大批短生命周期 goroutine 的“创建—执行—汇合”churn：每批 96 个 goroutine，每个在自己的栈上构造一条 3~7 个节点的堆指针链；全部 goroutine 在 release 同步点阻塞后，主协程分配垃圾并显式 `runtime.GC()` 两次，再放行。每个 goroutine 唤醒后重新遍历指针链，把长度与校验和发回主协程逐 id 比对。默认 6 批，共 576 个 goroutine、约 3000 个堆节点。

# 期望行为

1. 每个 goroutine 在 GC 前、GC 后遍历同一条指针链，长度与校验和一致；
2. 每个 worker id 恰好收到一个结果，无丢失、无重复；
3. 每批结果与主协程独立计算的期望值（节点数、节点值递增 7、校验和）完全一致；
4. 所有 goroutine 阻塞在同步点时发生显式 GC，栈上唯一的链头指针被正确扫描；
5. 最后一行输出 `ALL PASS l04_goroutine_churn`。

# 失败判读

- `chain changed across GC`：阻塞点栈上的链头指针未被扫描或指向已回收对象（UAF）；
- `chain mismatch`：节点内容被破坏或节点数量丢失；
- `missing result`：goroutine 未汇合或结果 channel 丢失（调度/栈问题）；
- 仅在 stress/k8s 下失败：高频 GC 与栈扫描路径问题，而非并发协议问题。

# 参数

- `UAF_SEED`：链长与节点值种子（默认 1），失败信息中的数值随种子变化；
- `TORTURE_ROUNDS`：批数，默认 6，范围 1–50；
- `TORTURE_WORKERS`：每批 goroutine 数，默认 96，范围 8–512。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_goroutine_churn/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=10 TORTURE_WORKERS=192 GOGC=1 /tmp/case
```
