---
id: l02_context_cause_chain
dim: L02_concurrency
priority: P0
sources:
  - go@1.24.1: src/context/x_test.go:577 TestCause；:812 TestCauseRace；:926 TestCustomContextCause
  - grpc-go@v1.71.1: test/context_canceled_test.go:37 deadline 与状态写竞争，状态与 trailer 必须一致
  - tailscale@v1.80.2: logtail/logtail_test.go:24 已取消 ctx 上 Shutdown 立即返回
tags: [L02, context, WithCancelCause, WithDeadlineCause, WithTimeoutCause, cause-propagation, first-cause-wins, gc, UAF_SEED]
requires: []
runtime: "<2s"
---

# 场景

模拟 Go 1.20+ 的 context cause 语义及 grpc 取消竞态：构造 parent → child/sibling → grandchild 的取消树，6 个等待者阻塞在 child.Done 上。主 goroutine 在确认全部等待者就位并执行 GC 后，先以 `errChild` 取消 child，再以 `errParent` 取消 parent，验证：

- child 的首个 cause 获胜，不被后续 parent cause 覆盖；
- sibling 通过 parent 继承 `errParent`；
- grandchild 通过 child 继承 `errChild`；
- 过期 `WithDeadlineCause` / 负超时 `WithTimeoutCause` 保留自定义 cause，`Err()` 为 DeadlineExceeded；
- `CancelCauseFunc(nil)` 的 Cause 回退为 `context.Canceled`；
- 父已取消后创建的子 context 继承父 cause，后续 cancel 不覆盖。

# 期望行为

1. 6 个等待者在 child 取消后全部醒来，Cause 均为 `errChild`、Err 为 Canceled；
2. child 首因获胜，parent/sibling/grandchild 的 cause 分别符合树形传播；
3. deadline/timeout 的 Err 为 DeadlineExceeded，Cause 为对应自定义错误；
4. nil cause 返回 `context.Canceled`；Background 的 Cause 为 nil；
5. 等待者 payload 跨 GC 完整；
6. 最后一行 `ALL PASS l02_context_cause_chain`。

# 失败判读

- `cause=...`：cause 传播/首因获胜逻辑错误，或 CancelCause 未生效；
- `Err()=... want=DeadlineExceeded`：deadline/timeout cause 构造错误；
- `waiter ... payload corrupted`：等待者持有的指针被 GC 破坏；
- 超时：Done 未关闭，或等待者未被广播唤醒。

# 参数

- `UAF_SEED`：决定等待者 payload 派生值；
- 无 TORTURE 参数（固定 6 等待者与 4 条 cause 路径；过期 deadline 使用过去时间点，不依赖墙钟等待）。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_context_cause_chain/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
