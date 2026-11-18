---
id: l03_append_growth_loop
dim: L03_async_preempt
priority: P2
sources:
  - gin@v1.10.0: tree_test.go:909 Params 以 cap=0 传入，append 必须跨容量边界扩容并返回 1 个参数
  - echo@v4.13.4: context_test.go:656 复用上下文时 paramNames/paramValues 按 maxParam 扩容/截断
tags: [L03, async-preempt, append, slice-growth, pointer-slice, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

每轮从 cap=0 的 `[]*node` 开始 append 1~64 个堆节点指针，跨越多次容量增长；每 32 轮在切片仍存活时显式 GC 并分配垃圾，GC pump 并发执行。每轮 append 后遍历校验元素，最近 8 个切片保留到结束再逐元素复核。

# 期望行为

1. 每轮 `len == n` 且 `cap >= n`；
2. append 后的每个指针都指向预期节点，字段在 GC 后正确；
3. 全部轮次的元素校验和与参考值一致；
4. 元素总数等于各轮长度之和；
5. 主循环显式 GC 次数符合 `(rounds-1)/32+1`；
6. 保留的 8 个切片在最终 GC 后仍逐元素正确；
7. GC pump 至少完成一轮，退出码 0，最后一行 `ALL PASS l03_append_growth_loop`。

# 失败判读

- `FAIL: slice length/capacity after growth`：扩容逻辑或边界计算错误；
- `FAIL: appended pointers/values readable after GC`：扩容期间指针未正确标记/复制；
- `FAIL: retained slices intact after final GC`：底层数组被回收或元素被覆盖；
- `SIGSEGV`：扩容后旧底层数组中的指针悬挂。

# 参数

- `UAF_SEED`：决定节点步长与起始偏移，失败信息带出种子；
- `TORTURE_ROUNDS`：放大主循环轮数，默认 4096。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_append_growth_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=65536 UAF_SEED=2 /tmp/case
```
