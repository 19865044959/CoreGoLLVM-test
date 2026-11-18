---
id: l04_context_cancel_tree
dim: L04_scheduler_stack
priority: P0
sources:
  - grpc-go@v1.71.1: test/context_canceled_test.go:37 deadline/cancel 与状态写出竞争，取消后 Done 关闭且只产生一个终态
  - quic-go@v0.50.1: streams_map_outgoing_test.go:456 每个等待者监听独立取消信号，取消者从等待队列移除，其余等待者不受影响
tags: [L04, context, cancel, propagation, broadcast, blocking, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

构造 `root -> 3 child -> 6 grandchild` 的 context 树，每个节点一个 goroutine 阻塞在 `ctx.Done()` 上并持有私有指针链。全部就绪后 GC；先取消中间 child：其自身与两个 grandchild 必须退出，root/兄弟子树不得受影响；再取消 root，其余全部退出。

# 期望行为

1. 取消 child1 后，child1 及其两个 grandchild 各退出一次；
2. 此时 `root.Err() == nil`、兄弟 child0/child2 及其 grandchild 的 `Err() == nil`；
3. 取消 root 后剩余 7 个 goroutine 全部退出，每个节点退出计数恰为 1；
4. 所有 goroutine 在取消时捕获的链（v, v+1）在最终 GC 后完整；
5. 最后一行输出 `ALL PASS l04_context_cancel_tree`。

# 失败判读

- 取消后部分 goroutine 未退出：Done channel 广播丢失或 ctx 树传播断裂；
- 兄弟子树提前退出：取消错误地向上/横向传播；
- 退出计数 >1：Done 被重复关闭或 goroutine 被重复拉起；
- “chain corrupted on cancel”：阻塞在 Done 上时栈/闭包指针未被扫描；
- 超时：某个 ctx 未被取消或 Done 未被唤醒。

# 参数

- `UAF_SEED`：节点链基值（默认 1）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_context_cancel_tree/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=5 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
