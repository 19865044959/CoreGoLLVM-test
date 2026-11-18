---
id: l03_defer_closure_chain
dim: L03_async_preempt
priority: P1
sources:
  - fiber@v2.52.6: app_test.go:1702 handler 内触发 Goexit 时依赖 defer 闭包完成清理，进程不得永久挂起
  - gorm@v1.30.0: tests/transaction_test.go:301 深层嵌套事务 + 包装回调，依赖 defer 路径完成回滚与资源清理
tags: [L03, async-preempt, defer, closure, recursion, LIFO, pointer-capture, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

每轮调用 8 层 noinline 递归；每层登记一个捕获当前堆节点指针的 defer 闭包，最深层周期性分配垃圾并显式 GC，随后逐层返回时闭包按 LIFO 执行并累加。主循环另有周期性 GC，GC pump 并发执行。与现有 per-call/主函数累积 defer 用例不同，这里检查的是跨多层栈帧的 defer 闭包链。

# 期望行为

1. defer 闭包总执行次数等于轮数 × 8；
2. 每轮闭包和等于 `8*node.v + 36`，总和与参考值一致；
3. 每轮返回值等于 `node.v + 8`；
4. 最深层 GC 与主循环 GC 计数符合固定公式；
5. GC pump 至少完成一轮；
6. 全部节点在 GC 后保持不变；
7. 退出码 0，最后一行 `ALL PASS l03_defer_closure_chain`。

# 失败判读

- `FAIL: defer closure count`：defer 记录在递归返回/栈增长时丢失；
- `FAIL: defer closure LIFO sum`：闭包捕获的指针或 depth 值恢复错误；
- `FAIL: defer-chain return sum`：返回值路径被 GC 破坏；
- `SIGSEGV` / `fatal error: found bad pointer`：defer 闭包中的指针未被扫描。

# 参数

- `UAF_SEED`：决定节点步长与起始偏移，失败信息带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_defer_closure_chain/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
