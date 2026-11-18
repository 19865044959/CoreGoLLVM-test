---
id: l03_branch_switch_holdptr
dim: L03_async_preempt
priority: P0
sources:
  - grpc-go@v1.71.1: test/context_canceled_test.go:37 deadline 与状态/trailer 写入竞争，两种结果路径都必须保持一致
  - quic-go@v0.50.1: frame_sorter_test.go:1377 1000 个帧乱序推送、每 N 次 Push 强制 GC，按 offset 还原数据流
tags: [L03, async-preempt, switch-branch, pointer-liveness, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

主 goroutine 在长循环内按 `i&3` 走 switch 四个分支：直接解引用、沿 `next` 跳一次、按 tag 走 if/else、再按 `(v+i)&3` 走内层 switch。每个分支都持有 `*bNode` 并读取相邻节点，循环体没有函数调用，依赖循环回边与分支汇合处的安全点被异步抢占。另一 goroutine 并发分配垃圾并执行 8 轮 `runtime.GC()`。节点访问顺序由 `UAF_SEED` 派生的奇数步长与偏移决定。

# 期望行为

1. 并发 GC pump 至少完成一轮（`epoch >= 1`）；
2. 循环至少执行 131072 次，四个外层分支与内层四个 case 均被覆盖；
3. 累加和等于按同一分支公式、只做整数推算得到的参考值；
4. GC 后所有节点的 `v`、`tagEven`、`next` 均未改变；
5. 进程退出码为 0，最后一行 `ALL PASS l03_branch_switch_holdptr`；
6. 各 profile（含 `asyncpreemptoff=0` 的 k8s）结果一致。

# 失败判读

- `FAIL: GC completed during branch/switch loop (seed=...)`：循环期间 GC 未完成，通常是异步抢占或循环安全点问题；
- `FAIL: branch/switch checksum (seed=...)`：分支体读取的指针字段被破坏或控制流被错误重排；
- `FAIL: branch-loop nodes intact after GC (seed=...)`：节点或 `next` 指针被错误回收/改写；
- 超时：紧循环与 GC 互相等待。

# 参数

- `UAF_SEED`：决定步长与起始偏移，默认 1，失败输出带出；
- 无墙钟断言；`bCapIters = 1<<24` 仅用于防挂起。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_branch_switch_holdptr/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=7 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1,asyncpreemptoff=0 /tmp/case
```
