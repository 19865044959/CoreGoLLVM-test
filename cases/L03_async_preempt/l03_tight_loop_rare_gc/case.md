---
id: l03_tight_loop_rare_gc
dim: L03_async_preempt
priority: P0
sources:
  - tailscale@v1.80.2: net/art/table_test.go:578 1 万次查找长循环中插入 runtime.GC() 校验结果一致
  - quic-go@v0.50.1: frame_sorter_test.go:1377 每 N 次 Push 强制 GC，校验乱序帧重组结果
tags: [L03, async-preempt, tight-loop, noinline, periodic-gc, safepoint]
requires: []
runtime: "<3s"
---

# 场景

模拟“长循环里每 N 轮才遇到一个安全点”的运行形态：主循环遍历 `*[128]node`，每 64 次迭代调用一次 `//go:noinline` 的 `gcTick`，该函数分配垃圾并执行 `runtime.GC()`。循环的其余部分是持指针的无调用块。

# 期望行为

1. `gcTick` 恰好被调用 `iters/tickEvery = 64` 次（调用未被内联或消除）；
2. 每次 GC 返回后继续循环，最终累加和等于逐项参考和；
3. 全部 128 个 `node` 的 `v`/`tag` 字段在多次 GC 后保持不变；
4. 进程退出码为 0，最后一行 `ALL PASS l03_tight_loop_rare_gc`；
5. 各 profile 下结果一致。

# 失败判读

- `FAIL: noinline GC call count`：调用被优化/内联异常，或循环提前退出；
- `FAIL: checksum after periodic noinline GC`：GC 期间存活指针或局部变量被破坏；
- `FAIL: node array intact after GC`：底层数组被错误回收或写屏障缺失；
- 超时：GC 调用路径死锁。

# 参数

无随机路径。`iters`、`tickEvery` 固定，不使用墙钟阈值。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_tight_loop_rare_gc/*.go
GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
