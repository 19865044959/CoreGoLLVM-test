---
id: l04_stack_shrink_unwind
dim: L04_scheduler_stack
priority: P2
sources:
  - go1.24.1: src/runtime/stack_test.go:952 growAndShrinkStack 先增长栈再收缩并校验帧指针
  - go1.24.1: src/runtime/stack_test.go:945 growAndShrinkStack(512, ...) 的 512 层增长调用点
tags: [L04, stack-shrink, panic, recover, unwind, stack-copy, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 3 轮，TORTURE_ROUNDS 可放大）"
---

# 场景

模拟“深递归出错后 panic 回卷，再由 GC 收缩栈”的路径：同一 worker goroutine 反复执行 400~479 层递归，每帧声明 64 个指针槽（全部指向 4 节点共享链）。叶帧显式 GC 后 panic，payload 携带叶深、槽位校验和与链指针；worker 在 defer 中 recover，panic 沿全部深栈帧回卷到浅栈。recover 后主协程在一次“浅栈停滞”处再 GC，触发 runtime 的 shrinkstack 路径，然后放行下一轮，再次增长/回卷。轮数由 `TORTURE_ROUNDS` 放大。

# 期望行为

1. 每轮 panic 都恰好被 recover，payload 类型为 `*suPayload`；
2. 每轮 payload.depth 等于目标深度，payload.sum 等于该深度 64 个槽位的独立校验和；
3. payload 链指针身份不变，回卷后链值未损坏；
4. 回卷到浅栈后显式 GC 不改变共享链，下一轮可重新增长到完整深度；
5. 全部轮次结束后最终 GC，共享链 4 个节点值均不变；
6. 最后一行输出 `ALL PASS l04_stack_shrink_unwind`。

# 失败判读

- `deep recursion returned without panic`：叶帧未到达或深度参数错误；
- payload 类型/校验和/链身份错：panic 回卷或栈复制破坏快照指针；
- `payload chain missing`：recover payload 中的堆指针在回卷/GC 后失效；
- `shared chain node ... corrupted`：栈收缩 GC 后已保留链被破坏；
- 进程崩溃未 recover：栈展开/defer 链异常；
- 超时：pause/resume 协调丢失。

# 参数

- `UAF_SEED`：深度（400 + seed%80）、链值步长（默认 1）；
- `TORTURE_ROUNDS`：轮数倍率，总轮数 = 3 × 该值（默认 1，即 3 轮）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_stack_shrink_unwind/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=6 TORTURE_ROUNDS=8 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
