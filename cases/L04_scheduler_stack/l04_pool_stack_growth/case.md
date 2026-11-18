---
id: l04_pool_stack_growth
dim: L04_scheduler_stack
priority: P1
sources:
  - caddy@v2.10.0: modules/caddyhttp/templates/tplcontext_test.go:190 嵌套 include 递归链与 sync.Pool 缓冲区，栈随 include 深度增长
  - nats-server@v2.11.0: server/norace_test.go:8101 多个发送者共享 worker/route 池，池内 goroutine 跨批次复用
tags: [L04, worker-pool, stack-growth, morestack, stack-copy, stack-shrink, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

固定 worker 池（默认 6 个 goroutine）跨多波任务复用。每个任务在 worker 自己的栈上执行约 230+ 层递归，每层声明 16 个指针槽，叶帧再声明 2048 个指针槽；指针槽全部指向任务私有的 4 节点链。所有 worker 同时停在叶帧协调点（`peak` -> `release`）时，主协程显式 GC；返回后再次 GC，触发 worker 栈收缩；下一波由同一批 goroutine 重新增长。最后一个任务结束后校验所有保留链。

# 期望行为

1. 每波每个任务的递归返回值等于按帧布局独立计算的期望和；
2. 叶帧 GC 后逐槽校验指针身份（`frame[i] == chain[(i+level+rot)&3]`），不得错位或变成标量；
3. worker 返回并阻塞在任务 channel 上时 GC 后，下一波仍能得到正确结果（栈收缩后重新增长）；
4. 每个任务只看到自己的链，跨任务不串号；所有任务和与期望总和一致；
5. 最后一行输出 `ALL PASS l04_pool_stack_growth`。

# 失败判读

- 返回 -2^60：某帧指针槽在 morestack/栈复制或收缩 GC 后错位；
- 某任务结果等于相邻任务期望：worker 复用时栈上残留数据串到新任务；
- `SIGSEGV` / `found bad pointer`：大叶帧 stack map 漏标指针；
- 仅在 `GOGC=1 GOMAXPROCS=2` 下失败：栈复制与并发 GC 的交互问题。

# 参数

- `UAF_SEED`：链基值与递归深度扰动（默认 1）；
- `TORTURE_WORKERS`：池大小（默认 6，范围 2..16）；
- `TORTURE_ROUNDS`：波次数（默认 4，范围 1..12）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_pool_stack_growth/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 TORTURE_ROUNDS=8 TORTURE_WORKERS=8 GOGC=1 GOMAXPROCS=2 /tmp/case
```
