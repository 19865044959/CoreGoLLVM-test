---
id: l04_stack_grow_interleaved_gc
dim: L04_scheduler_stack
priority: P1
sources:
  - go1.24.1: src/runtime/stack_test.go:79 TestStackGrowth 覆盖 morestack/栈复制路径
  - go1.24.1: src/runtime/stack_test.go:183 TestStackGrowthCallback 在栈增长过程中执行回调
tags: [L04, morestack, stack-growth, stack-copy, stackmap, interleaved-gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟深层配置/模板递归在增长过程中反复遭遇 GC：单 goroutine 递归 768~895 层，每帧声明 96 个指针槽（768B）并新建一个堆节点，槽位指向 8 个共享节点加本帧新节点，栈从 8KB 多次 morestack 增长到约 0.7MB。每 128 层在“只增长了一部分”的栈上执行显式 GC（含垃圾分配），GC 后继续向更深层增长，随后回溯；每一帧在两个时点逐槽校验指针身份：mid-growth GC 之后、子帧回溯之后。最终再 GC 并复查全部新建节点。

# 期望行为

1. 递归返回值与独立公式（每层 95 个共享槽 + 1 个新节点值）一致；
2. 至少发生 5 次 mid-growth 显式 GC；
3. 每次 mid-growth GC 后，当前帧 96 个槽位指针身份与期望完全一致；
4. 子帧返回后当前帧槽位仍一致，所有帧的新节点身份/值未变；
5. 最终 GC 后 768~895 个新建节点与 8 个共享节点全部存活且值正确；
6. 最后一行输出 `ALL PASS l04_stack_grow_interleaved_gc`。

# 失败判读

- 槽位指向错误节点：栈复制/stack map 在增长中途漏标指针；
- 返回和错但槽位正常：morestack 后局部变量值损坏；
- `post-GC fresh node ... missing`：堆节点只被生长中的栈帧引用却未被保留；
- GC 次数不足：阶段边界/深度计算被随机参数破坏；
- `SIGSEGV` / `found bad pointer`：栈映射漏标指针槽。

# 参数

- `UAF_SEED`：总深度（768 + seed%128）、新节点步长与共享节点值（默认 1）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_stack_grow_interleaved_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=11 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
