---
id: l04_goroutine_stack_growth
dim: L04_scheduler_stack
priority: P1
sources:
  - frp@v0.67.0: pkg/config/load_test.go:59 生成 10k 层嵌套 JSON，解码为 map 后递归反射遍历，以深度触发 morestack/栈复制
  - caddy@v2.10.0: modules/caddyhttp/templates/tplcontext_test.go:190 嵌套 include 递归链与 sync.Pool 缓冲区
tags: [L04, morestack, stack-growth, stack-copy, goroutine, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 frp 深层配置反射遍历与 Caddy 嵌套模板 include 的栈增长路径：每轮新建一个 goroutine，从 `growL1` 依次调用 `growL2..growL5`，各层分别声明 2/4/8/16/32KB 的指针槽数组（合计约 62KB），全部指向同一条 4 节点链。最深层 `growL5` 发出 ready 后停在 channel 接收上，此时五层帧同时存活，主协程显式 GC 两次；放行后逐层回溯复验本层数组。默认 6 轮，每轮从新的小初始栈开始，再次触发多次 morestack/栈复制。

# 期望行为

1. 每轮最深层阻塞点发生 GC 时，五层指针数组全部位于活跃栈帧并被正确扫描；
2. 回溯时每一层数组的槽位仍严格等于 `chain[(i+level)&3]`（指针身份）；
3. 每轮各层校验和之和等于 `(总槽数/4)*chainSum`；
4. 6 轮结果一致，所有 goroutine 正常退出；
5. 全部轮次结束后的 GC 不破坏主协程持有的链；
6. 最后一行输出 `ALL PASS l04_goroutine_stack_growth`。

# 失败判读

- `outer/inner frame pointer corrupted`：栈增长/复制时外层帧指针槽未修正或栈映射漏标；
- `sum=... want=...`：数组槽位被破坏、层级索引错误或 GC 清零；
- 超时：最深层未发出 ready，或 release 未关闭；
- 仅 stress/k8s 下失败：GC 在栈复制过程中扫描大帧的时序问题。

# 参数

- `UAF_SEED`：链基值种子（默认 1）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_goroutine_stack_growth/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=2 GODEBUG=efence=1 /tmp/case
```
