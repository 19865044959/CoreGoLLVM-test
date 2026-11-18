---
id: l03_atomic_spin_loop
dim: L03_async_preempt
priority: P0
sources:
  - tailscale@v1.80.2: syncs/shardvalue_test.go:16 1000×10000 原子分片计数长循环，穿插 GC，总和必须守恒
  - nats-server@v2.11.0: server/norace_test.go:11455 有界队列 + 非阻塞/轮询发送 + 运行时开关切换
tags: [L03, async-preempt, atomic, spin-loop, gc, preemption]
requires: []
runtime: "<3s"
---

# 场景

主 goroutine 在 atomic 自旋循环里等待 GC worker 放行：自旋体只有原子 load、指针解引用和累加，没有函数调用；worker 先等主 goroutine 进入自旋（`entered`），再执行 8 轮“分配垃圾 + `runtime.GC()`”，最后写入 `gate`。放行后主 goroutine 做固定 64K 次原子累加。

# 期望行为

1. `gate` 被 worker 置 1，自旋循环实际执行过（`spins > 0`）；
2. 自旋累加值与闭式参考一致（64 个元素按周期求和）；
3. 放行后的原子累加和等于固定期望值；
4. GC 后底层数组内容不变；
5. 进程退出码为 0，最后一行 `ALL PASS l03_atomic_spin_loop`；
6. 各 profile 下结果一致（k8s 的 `asyncpreemptoff=0` 下尤其关键）。

# 失败判读

- `FAIL: GC worker released atomic gate`：worker 未完成 GC 或自旋未被异步抢占，主 goroutine 无法继续；
- `FAIL: spin loop executed`：进入自旋的握手未被观察到，调度顺序异常；
- `FAIL: spin-loop pointer checksum` / `post-spin atomic accumulation`：GC 中断后寄存器/局部指针恢复错误；
- `FAIL: pointer target intact after spin GC`：持有指针未保活；
- 超时：自旋与 GC 相互等待（异步抢占失效时最典型）。

# 参数

无随机路径。`guardSpins=1<<30` 仅作防挂起上限，正常路径远达不到；不使用墙钟阈值。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_atomic_spin_loop/*.go
GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1,asyncpreemptoff=0 /tmp/case
```
