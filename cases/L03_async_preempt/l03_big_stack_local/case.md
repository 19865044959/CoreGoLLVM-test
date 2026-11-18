---
id: l03_big_stack_local
dim: L03_async_preempt
priority: P2
sources:
  - etcd@v3.5.0: pkg/adt/interval_tree_test.go:359 随机区间操作 + 递归 Visit，制造递归栈压力
  - nats-server@v2.11.0: test/service_latency_test.go:1226 递归解析导入链（原栈溢出回归用例）
tags: [L03, async-preempt, large-stack-frame, stack-growth, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

循环每轮调用 `//go:noinline` 的 `bigStackFrame`：函数内声明 `[2048]int64`（16KiB）栈上数组，填充后再交给另一个 noinline 函数求和；每轮调用都会触碰大栈帧。每 16 轮在浅栈上分配垃圾并显式 `runtime.GC()`，使栈有机会收缩，下一轮重新增长；GC pump 并发执行 8 轮 GC。默认 512 轮，`TORTURE_ROUNDS` 可放大。

# 期望行为

1. 并发 GC pump 至少完成一轮；
2. 默认 512 轮（或 `TORTURE_ROUNDS` 指定轮数）全部执行；
3. 累加和等于按大栈帧内容的闭式公式推算的参考值；
4. GC 后全部节点字段不变；
5. 进程退出码为 0，最后一行 `ALL PASS l03_big_stack_local`；
6. 各 profile 下结果一致。

# 失败判读

- `FAIL: GC pump completed during big-stack loop`：每轮大栈帧调用期间 GC 未完成；
- `FAIL: big-stack checksum (seed=...)`：栈上数组内容被破坏或大帧被错误复用；
- `FAIL: big-stack nodes intact after GC (seed=...)`：节点指针未保活；
- 崩溃/超时：栈增长、栈复制或 GC 扫描栈帧异常。

# 参数

- `UAF_SEED`：节点访问步长、偏移与帧内 salt，默认 1，失败输出带出；
- `TORTURE_ROUNDS`：循环轮数（默认 512）；每轮 16KiB 栈帧，放大时线性增长。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_big_stack_local/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=7 TORTURE_ROUNDS=8192 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
