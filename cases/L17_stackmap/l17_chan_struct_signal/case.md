---
id: l17_chan_struct_signal
dim: L17_stackmap
priority: P2
sources:
  - nats-server@v2.11.0: server/events_test.go:629（done := make(chan struct{})，goroutine 循环 select 退出）
  - nats-server@v2.11.0: server/filestore_test.go:5114（qch := make(chan struct{}) + defer close 广播 10 个 worker）
tags: [L17, stackmap, chan-struct, broadcast, close, goroutine-stack, noinline, TORTURE_ROUNDS, GC, UAF_SEED]
requires: []
runtime: "<10s（默认 24 轮 × 4 worker；TORTURE_ROUNDS 放大轮数，上限 4096）"
---

# 场景

全部同步信号使用零大小元素 `chan struct{}`：worker 在栈上建立指针链与含 `[]*item` 的 holdSet，先发 ready（无缓冲 chan struct{}）、再阻塞在 `<-release`。main 收齐全部 ready 后执行 GC，然后 `close(release)` 广播唤醒所有 worker；worker 在唤醒后两轮 GC 并校验链，最后发 done。另有一段“已关闭 chan struct{} 广播”路径，3 个等待者同时被唤醒后校验各自栈上指针链。

`UAF_SEED` 决定每轮 base；`TORTURE_ROUNDS` 放大轮数。

# 期望行为

1. 每个 worker 的 8 节点链在 release 前后两轮 GC 后值/长度完全一致；
2. holdSet 中的 `list` 指针与 tag 在 GC 后仍有效；
3. main 在 worker 阻塞期间 GC 不导致任何指针链被回收；
4. close 广播路径中 3 个等待者的链 checksum 全部正确；
5. 退出码 0，最后一行 `ALL PASS l17_chan_struct_signal`。

# 失败判读

- `worker ... chain`：阻塞接收帧中的指针链未被栈图覆盖，或 close 唤醒路径漏扫；
- `hold set/list values corrupted`：局部聚合/切片字段被回收；
- `slot=... want=...`：worker 校验和错误，可能是指针失效或 worker 未完成；
- 挂起超时：ready/release/done 信号配对逻辑异常；
- 仅 clobberfree 下失败：worker 栈槽漏标。

# 参数

- `UAF_SEED`：每轮 base 种子，失败输出带该值；
- `TORTURE_ROUNDS=N`：默认 24 轮，上限 4096。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_chan_struct_signal/*.go
UAF_SEED=1 TORTURE_ROUNDS=64 GODEBUG=clobberfree=1 /tmp/case
```
