---
id: l04_stack_sawtooth_cycles
dim: L04_scheduler_stack
priority: P1
sources:
  - caddy@v2.10.0: modules/caddyhttp/replacer_test.go:26 深占位符/嵌套花括号放大触发 morestack 与栈复制
  - frp@v0.67.0: pkg/config/load_test.go:59 深层嵌套配置解码后的递归遍历以深度触发栈增长
tags: [L04, morestack, stack-copy, stack-shrink, sawtooth, recursion, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 Caddy 深占位符展开与 frp 深配置递归遍历叠加出的栈"锯齿"：同一个 worker goroutine 连续 6 轮，每轮交替进行深递归（180 层）与浅递归（24 层）。递归帧按 A/B/C 三种大小（768/192/48 个指针槽，6KB/1.5KB/384B）轮转，叶帧额外声明 1024 个指针槽（8KB）。在叶帧所有父帧指针槽都填充后阻塞，主协程 GC（峰值栈约 500KB）；返回后低栈处再 GC，给 runtime 收缩同一 goroutine 栈的机会。回溯时逐槽校验指针身份。

# 期望行为

1. 每轮递归返回值等于独立公式 `sawWant(depth, Σchain)`；
2. 峰值 GC 后回溯时，每层帧的全部指针槽严格等于 `chain[(i+level)&3]`（指针身份）；
3. 栈顶叶帧阻塞期间，整条增长中的栈被 GC 正确扫描，无 -2^60 哨兵值；
4. 返回后低栈 GC 不破坏主协程持有的 4 节点链，链值仍为 `b..b+3`；
5. 进程退出码为 0，最后一行输出 `ALL PASS l04_stack_sawtooth_cycles`。

# 失败判读

- `res=... want=...`：某层帧数据或返回值在栈复制/收缩中损坏；
- `pointer slots corrupted`：栈槽位图漏标，指针身份不符；
- `chain ... corrupted`：主协程持有的锚点链被误回收；
- 栈溢出/超时：递归深度或帧大小异常放大。

# 参数

- `UAF_SEED`：每轮链值种子；
- 固定 6 轮、两种深度交替，无 TORTURE 放大参数。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_stack_sawtooth_cycles/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
