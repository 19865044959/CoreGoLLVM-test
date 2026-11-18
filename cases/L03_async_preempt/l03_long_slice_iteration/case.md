---
id: l03_long_slice_iteration
dim: L03_async_preempt
priority: P1
sources:
  - nats-server@v2.11.0: test/fanout_test.go:34 大量订阅者长循环有序扇出，分发过程中插入 GC
  - quic-go@v0.50.1: frame_sorter_test.go:1377 1000 个帧长循环重组并逐字节校验，期间强制 GC
tags: [L03, async-preempt, slice-iteration, pointer-array, gc, stack-map]
requires: []
runtime: "<10s"
---

# 场景

32K 元素的 `[]*node` 指针切片反复遍历求和，每个元素指向同一块堆上 backing 数组。另一个 goroutine 并发执行多轮“分配垃圾 + `runtime.GC()`”。主 goroutine 在每轮遍历之间读一次原子 epoch，至少完成一轮 GC 且遍历 3 轮后才退出。

# 期望行为

1. 遍历期间至少一轮 GC 完成（`epoch >= 1`）；
2. 总校验和等于 `passes * base`，其中 `base` 为全部节点值之和；
3. GC 后所有 32K 节点仍可解引用，`v`/`s` 字段不变；
4. 进程退出码为 0，最后一行 `ALL PASS l03_long_slice_iteration`；
5. 各 profile 下结果一致。

# 失败判读

- `FAIL: GC completed during slice traversal`：长遍历未在安全点被抢占/GC 未完成；
- `FAIL: slice-pointer checksum across GC`：指针切片元素被破坏或提前失效；
- `FAIL: all nodes intact after GC`：backing 数组被回收（clobberfree/efence 下更明显）；
- 超时：背景 GC 与遍历互锁。

# 参数

无随机路径。遍历上界 `passes < 1024` 只用于防挂起；正常路径 `passes` 为 3 到数次。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_long_slice_iteration/*.go
GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1,asyncpreemptoff=0 /tmp/case
```
