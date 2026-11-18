---
id: l03_tight_loop_no_calls
dim: L03_async_preempt
priority: P0
sources:
  - tailscale@v1.80.2: syncs/shardvalue_test.go:16 1000 goroutine × 10000 原子分片计数长循环，GC 穿插
  - nats-server@v2.11.0: server/sublist_test.go:897 1000 次递归匹配长循环中周期性 runtime.GC()
tags: [L03, async-preempt, tight-loop, safepoint, stack-map, gc]
requires: []
runtime: "<3s"
---

# 场景

模拟事件循环里无函数调用的紧循环：主 goroutine 在循环内持续持有 `*[64]int64`（指向堆上 slice 的底层数组），循环体只做取数与整数累加，没有任何调用。另一个 goroutine 反复分配垃圾并执行 `runtime.GC()`。主循环跑到至少一轮完整 GC 结束后才退出，以此覆盖 SIGURG 异步抢占、循环回边安全点与栈/寄存器指针位图。

# 期望行为

1. 在并发 GC 完成前主循环持续运行（`epoch >= 1` 后才退出）；
2. 分块累加的校验和等于 `done*64`（每 64 次迭代的闭式值 4096）；
3. 所有 GC 结束后 `*[64]int64` 的 64 个元素仍等于初始值；
4. 进程退出码为 0，最后一行 `ALL PASS l03_tight_loop_no_calls`；
5. 在 clobberfree / efence / stress / k8s 等 profile 下结果一致。

# 失败判读

- `FAIL: GC completed during tight loop`：GC 未能在循环期间完成，通常是异步抢占或循环安全点缺失；
- `FAIL: tight-loop checksum ...`：循环计算被破坏，检查栈映射/寄存器溢出恢复；
- `FAIL: pointer target intact after GC`：持有指针未被识别为存活，底层数组被回收（clobberfree/efence 下更易暴露）；
- 超时或挂起：GC 与紧循环互相等待，检查抢占路径。

# 参数

无随机路径。循环块大小与 GC 轮数固定，无墙钟阈值；`done` 上限 `1<<26` 仅用于防挂起，正常路径远达不到。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_tight_loop_no_calls/*.go
GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1,asyncpreemptoff=0 /tmp/case
```
