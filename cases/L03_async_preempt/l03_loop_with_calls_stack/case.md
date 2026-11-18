---
id: l03_loop_with_calls_stack
dim: L03_async_preempt
priority: P1
sources:
  - etcd@v3.5.0: pkg/adt/interval_tree_test.go:359 随机区间操作 + 递归 Visit，制造递归栈压力
  - nats-server@v2.11.0: test/service_latency_test.go:1226 递归解析导入链（原栈溢出回归用例）
tags: [L03, async-preempt, stack-growth, recursion, gc, stack-copy]
requires: []
runtime: "<10s"
---

# 场景

每轮循环调用 `//go:noinline` 的递归函数：每层 512×int64（4KiB）栈帧、深度 12，合计约 48KiB，迫使 goroutine 栈从初始大小多次增长与复制。返回后在浅栈上执行 `runtime.GC()`，GC 可使栈收缩，下一轮再次增长。主循环还持有堆指针 `q`，在 GC 调用前后各读一次；另有 goroutine 并发 GC pump。

# 期望行为

1. 循环期间至少一轮背景 GC 完成；
2. 递归返回值累加和等于闭式参考 `(depth+1)*v + depth*(depth+1)/2` 的逐轮和；
3. 堆指针在每次 `runtime.GC()` 前后读到的值一致，累加和等于两倍参考和；
4. GC 后所有节点值不变；
5. 进程退出码为 0，最后一行 `ALL PASS l03_loop_with_calls_stack`；
6. 各 profile 下结果一致。

# 失败判读

- `FAIL: GC completed during stack-growth loop`：GC 未在循环期间完成；
- `FAIL: recursive-call checksum across stack growth`：栈增长/复制过程中局部值被破坏；
- `FAIL: pointer value stable across GC calls`：GC 调用前后的指针读取不一致，栈映射或写屏障问题；
- `FAIL: nodes intact after stack-growth GC`：指针目标被回收；
- 崩溃（SIGSEGV / bad pointer）：栈复制时指针位图错误。

# 参数

无随机路径。递归深度、帧大小、循环次数与 GC 周期均为固定常量，不使用墙钟阈值。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_loop_with_calls_stack/*.go
GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
