---
id: l04_gosched_fairness
dim: L04_scheduler_stack
priority: P1
sources:
  - tailscale@v1.80.2: syncs/shardvalue_test.go:16 1000×10000 原子分片计数长循环与调度点
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:37 100 goroutine 并发调度 + 单消费者 FIFO
tags: [L04, Gosched, GPM, fairness, pointer, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 shardvalue/callback serializer 的高频调度：16 个 goroutine 各持有 3 节点堆指针链，循环 64 步，每一步都 `runtime.Gosched()` 让出；走到一半时全部阻塞在 release 同步点，主协程分配垃圾并显式 GC 两次，再放行。每个 worker 在阻塞前后与每一步都校验指针链长度/校验和，最终累加器与独立公式比对。GOMAXPROCS=2（k8s profile）下仍要求所有 worker 完成全部步数。

# 期望行为

1. 每个 worker 在每次让出前后遍历同一条 3 节点链，校验和恒为 `3*base+21`；
2. 全部 16 个 worker 都完成 64 步，累加器等于 `64*63/2 + 64*(3*base+21)`；
3. GC 发生在所有 worker 阻塞于 release 时，栈/寄存器中的链头指针与累加器被正确扫描；
4. 每个 id 恰好一个结果，无丢失/重复；
5. 最后一行输出 `ALL PASS l04_gosched_fairness`。

# 失败判读

- `pointer chain broken`：Gosched 抢占或 GC 后局部指针被破坏；
- `acc=... want=...`：让出/唤醒路径丢失累加步骤；
- `missing result`：worker 未完成或被调度器卡住；
- 仅 single/k8s 下失败：单 P 或低 GOMAXPROCS 的抢占/栈扫描问题。

# 参数

- `UAF_SEED`：链节点初值（默认 1）。
- 迭代次数固定（16 worker × 64 步），不使用时间阈值。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_gosched_fairness/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=1 GODEBUG=clobberfree=1 /tmp/case
```
