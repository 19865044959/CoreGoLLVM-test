---
id: l03_panic_recover_loop
dim: L03_async_preempt
priority: P1
sources:
  - gorm@v1.30.0: tests/transaction_test.go:301 深层嵌套事务 + 包装回调，回调 panic 时外层必须回滚
  - etcd@v3.5.0: pkg/pbutil/pbutil_test.go:31 TestMarshalerPanic：Must 封装在失败时 panic，由 recover 断言
tags: [L03, async-preempt, panic, recover, stack-unwind, payload-pointer, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

长循环中每 3 轮触发一次 panic：在 4 层 noinline 调用深处 panic 一个堆节点指针，最外层 `defer/recover` 取回载荷并解引用；部分轮次在 panic 前分配垃圾并显式 GC。另有 GC pump 并发执行 8 轮 GC。非 panic 轮返回普通计算结果。

# 期望行为

1. recover 次数等于 `r%3==0` 的轮数；
2. 每次 recover 到的载荷都是原始节点指针，字段完整；
3. panic 轮与正常轮的两路累加和均与参考值一致；
4. panic 路径内的显式 GC 次数符合 `r%291==0` 的轮数；
5. 主循环显式 GC 次数符合 `r%1000==0` 的轮数；
6. 全部节点在 GC 后保持不变；
7. 退出码 0，最后一行 `ALL PASS l03_panic_recover_loop`。

# 失败判读

- `FAIL: recover payload pointer intact`：panic 载荷指针在栈展开/GC 后失效，重点查 stack map 与 panic arg 标记；
- `FAIL: recovered pointer checksum` / `non-panic return checksum`：recover 后局部变量恢复错误或栈复制丢值；
- `FAIL: recover count`：panic 路径被错误优化或 defer 记录丢失；
- `SIGSEGV` / `fatal error: found bad pointer`：展开过程中的指针未被扫描。

# 参数

- `UAF_SEED`：决定节点步长与起始偏移，失败信息带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_panic_recover_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
