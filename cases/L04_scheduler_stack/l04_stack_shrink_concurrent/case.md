---
id: l04_stack_shrink_concurrent
dim: L04_scheduler_stack
priority: P2
sources:
  - etcd@v3.5.0: pkg/adt/interval_tree_test.go:359 随机区间插入/删除后的递归 Visit 造成栈增长
  - tailscale@v1.80.2: net/art/table_test.go:578 1 万前缀建表/查找长循环中穿插 GC 与调度点
tags: [L04, stack-shrink, stack-growth, morestack, stack-copy, concurrent, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认轮数，TORTURE_ROUNDS/TORTURE_WORKERS 可放大）"
---

# 场景

多个 worker 各自反复执行约 420 层、每层 24 个指针槽的深递归；每次返回后停在协调点，主协程 GC 促使 goroutine 栈收缩，随后再进入下一轮深递归。与此同时一个 anchor goroutine 一直停在更深（360 层 + 1024 指针叶帧）的叶帧上，跨所有收缩 GC 保持整条深栈活跃。所有轮次结束后放行 anchor 回溯。

# 期望行为

1. 每个 worker 每轮递归返回值和按帧布局独立计算的期望值一致（返回 -2^60 即为帧槽损坏）；
2. worker 返回后 GC 并再次深递归（增长路径）结果仍正确；
3. anchor 在所有 worker 收缩 GC 期间保持深栈阻塞，最终回溯时逐层/逐槽校验通过；
4. anchor 与 worker 的堆链在最终 GC 后完整；
5. 最后一行输出 `ALL PASS l04_stack_shrink_concurrent`。

# 失败判读

- 某轮 got=-2^60：栈收缩/复制后帧内指针槽错位；
- anchor 失败而 worker 通过：长时间活跃深栈的 stack map 或收缩边界问题；
- “kept[...] missing”：仅被局部/全局切片引用的链在 GC 后失效；
- 超时：协调点未放行（收缩后 goroutine 未被调度）；
- 仅 k8s/stress 下失败：GOMAXPROCS=2 与高频 GC 下的栈复制竞态。

# 参数

- `UAF_SEED`：链基值与 salt 扰动（默认 1）；
- `TORTURE_WORKERS`：并发 worker 数（默认 4，范围 1..12）；
- `TORTURE_ROUNDS`：每个 worker 的收缩轮数（默认 4，范围 1..12）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_stack_shrink_concurrent/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=13 TORTURE_ROUNDS=10 TORTURE_WORKERS=8 GOGC=1 GOMAXPROCS=2 /tmp/case
```
