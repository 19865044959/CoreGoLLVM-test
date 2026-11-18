---
id: l04_sleep_wake_pointer
dim: L04_scheduler_stack
priority: P1
sources:
  - tailscale@v1.80.2: net/memnet/pipe_test.go:33 读写 deadline 与 Block 唤醒路径
  - frp@v0.67.0: pkg/util/vhost/https_test.go:12 accept 循环 + deadline 后的 goroutine 唤醒与连接处理
tags: [L04, sleep-wake, timer, pointer, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 tailscale memnet 管道与 frp TLS 嗅探中“goroutine 睡眠等待 I/O/deadline 后被唤醒”的路径：8 个 worker 各自持有栈上 `*node` 和一个 8 槽堆切片，循环 24 次，每次写入新值后 `time.Sleep(1ms)`，唤醒后校验值未被破坏。第 12 次时全部 worker 阻塞于 release，主协程分配垃圾并 GC 两次，再放行。整个用例只按固定次数调度，不根据耗时判定。

# 期望行为

1. 每次睡眠唤醒后，`node.v` 与对应 `vals` 槽仍等于本 worker 刚写入的值；
2. 阻塞点 GC 前后，栈上 `*node` 与切片头指针保持有效；
3. 最终 `node.v == initial+24`，8 个切片槽分别等于最近一次写入值（独立公式校验）；
4. 8 个 worker 结果齐全，无丢失/重复；
5. 最后一行输出 `ALL PASS l04_sleep_wake_pointer`。

# 失败判读

- `corrupted across sleep+GC`：唤醒路径上栈指针被破坏或指向已回收对象；
- `final/vals mismatch`：睡眠期间数据丢失（栈槽未正确扫描/恢复）；
- `missing result`：worker 未唤醒或死锁；
- 仅 stress/k8s 下失败：GC 与定时器唤醒竞争问题。

# 参数

- `UAF_SEED`：节点/切片初值（默认 1）。
- 固定 8 worker × 24 次睡眠 × 1ms；不依赖墙钟阈值。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_sleep_wake_pointer/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
