---
id: l04_deep_recursion_stack
dim: L04_scheduler_stack
priority: P1
sources:
  - nats-server@v2.11.0: server/sublist_test.go:897 递归 subject trie 匹配 + 并发 + GC
  - nats-server@v2.11.0: test/service_latency_test.go:1226 多层转发导入链的递归解析与栈溢出回归
tags: [L04, morestack, stack-copy, recursion, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 NATS sublist 递归匹配与导入链解析的深调用：递归深度由 `UAF_SEED` 固定在 1800–2399 层，每层栈帧含 48 个指针槽（384B）和 3 个堆对象；递归到叶子触发一次显式 GC，之后每 257 层再触发一次。栈从 8KB 逐级增长到约 1MB，期间发生多次 morestack 栈复制。回溯时逐层校验 `buf[0]/buf[1]/buf[46]/buf[47]` 的指针身份与节点内容，并累加返回值与主协程独立公式比对。

# 期望行为

1. 每次递归调用的返回值等于 `expectedDeep(depth, salt)` 的公式值；
2. 回溯时每层 48 个指针槽仍指向本帧创建的 `top/hi/local` 对象，无 -2^60 哨兵值；
3. 叶子与每 257 层的显式 GC 发生在深栈活跃期间，栈复制后指针全部重定位正确；
4. 两轮不同深度/盐值的递归都通过；
5. 最后一行输出 `ALL PASS l04_deep_recursion_stack`。

# 失败判读

- 返回 -2^60：某一层的指针槽或节点内容在栈复制/GC 后被破坏；
- 返回值与公式不符但非哨兵：累加路径丢失某层数据，栈帧恢复不完整；
- `SIGSEGV` 或 `fatal error: found bad pointer`：栈复制时 stack map 漏标指针；
- 栈溢出：morestack 增长路径异常（结合 CoreGoLLVM 栈增长实现定位）。

# 参数

- `UAF_SEED`：决定递归深度（1800 + seed%600）与盐值，失败信息带出 depth/salt。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_deep_recursion_stack/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=5 GOGC=1 GODEBUG=clobberfree=1,asyncpreemptoff=0 /tmp/case
```
