---
id: l02_context_deadline_tree
dim: L02_concurrency
priority: P0
sources:
  - grpc-go@v1.71.1: test/context_canceled_test.go:37 deadline 与状态写竞争，状态与附属数据必须一致
  - etcd@v3.5.0: pkg/wait/wait_time_test.go:54 10k 个 deadline channel 单次广播全部唤醒
  - fasthttp@v1.59.0: server_test.go:3797 / 3838 context 超时返回 DeadlineExceeded，活跃连接保留
tags: [L02, context, deadline-tree, WithCancel, WithDeadline, propagation, DeadlineExceeded, gc]
requires: []
runtime: "<2s"
---

# 场景

显式构建 context 树并验证传播边界，全部使用固定绝对时刻，不做任何时长等待：

- root(WithCancel) 下挂 sibling 分支 sib→sibChild，以及 mid(WithDeadline(far))；
- mid 下挂 leaf1(WithCancel)、leaf2(WithDeadline(later))、expired(WithDeadline(过去时刻))；
- leaf2 的 later 晚于祖先 far，验证 WithDeadline 对更早祖先 deadline 的继承；
- expired 创建后立即处于 DeadlineExceeded/Done 已关闭状态。

7 个 goroutine 各等待一个节点 Done，启动后主 goroutine 做两轮显式 GC；随后先收割 expired，再 `cancel(mid)`，验证 mid 子树 4 个节点退出、root 分支 3 个节点不受影响；最后 cancel(root) 收割剩余节点。每个 waiter 在 Done 后 GC 并校验持有的 `*token`。

# 期望行为

1. expired 立即报 `DeadlineExceeded` 且 Done 关闭；
2. leaf1 继承 mid 的 deadline，leaf2 因祖先更早而同样报告 far；sibling 无 deadline；
3. cancel(mid) 后 mid、leaf1、leaf2 报 `context.Canceled`，root/sib/sibChild 仍为 nil；
4. mid 取消后新建的子 context 立即继承 Canceled；
5. cancel(root) 后剩余 3 个 waiter 全部收到 Canceled；所有 token 跨 GC 有效；
6. 最后一行 `ALL PASS l02_context_deadline_tree`。

# 失败判读

- `deadline` 断言失败：WithDeadline 的祖先优先规则或 Deadline() 继承被破坏；
- `was canceled by mid`：取消错误传播到兄弟分支；
- `err got=`：Done 关闭但 Err 原因错误；
- `token corrupted after GC`：waiter 持有的指针跨 GC 失效。

# 参数

- `UAF_SEED`：决定 token tag 派生值，失败输出带种子；
- 无 TORTURE 参数；deadline 使用固定绝对时刻（过去/远期），无 sleep。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_context_deadline_tree/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
