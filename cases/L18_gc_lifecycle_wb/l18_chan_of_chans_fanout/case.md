---
id: l18_chan_of_chans_fanout
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - nats-server@v2.11.0: server/ipqueue_test.go:253（TestIPQueueMultiProducers：通知 channel + 批量 pop + recycle）
  - etcd@v3.5.0: server/mvcc/watchable_store_test.go:532（TestWatchVictims：watch stream 的有界 channel 与慢消费者淘汰）
  - quic-go@v0.50.1: streams_map_outgoing_test.go:110（CloseWithError 时向全部 stream 广播关闭）
tags: [L18, chan-of-chans, buffered-channel, unbuffered-channel, packet-chain, GC, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

老 hub 持有已晋升的缓冲 chan（cap=16）与无缓冲 chan，元素类型都是 `chan *cfPkt`：

1. 阶段一：noinline `cfMakeAndSend` 创建 8 个年轻子 channel，各自预装 4 个年轻 packet（含 `next` 链）后送入老缓冲 chan；跨两轮 GC 后由 `cfRecvChan` 逐个取回，再 GC 并 drain 到老 hub 的 sink slice；
2. 阶段二：goroutine 在老无缓冲 chan 上交接 6 个年轻子 channel（每个 3 个 packet），每次发送前调用 `runtime.GC()`；主协程接收后立即 GC 再 drain；
3. 最终 GC 后复查 sink 首尾 packet 链。

# 期望行为

1. 老缓冲 chan 跨 GC 后仍能取回全部 8 个年轻子 channel，channel 长度与 packet 数正确；
2. 每个子 channel 内 packet 的 seq、32 字节 data、`next` 链在 GC 后完整且按 FIFO 顺序；
3. 老无缓冲 chan 交接 6 个子 channel 无丢失、无多余消息，close 后 range 正常结束；
4. sink 总数 50，首包与最后 cohort 的包链在最终 GC 后仍正确；
5. 失败信息带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_chan_of_chans_fanout`。

# 失败判读

- `old buffered chan len=...`：老 chan 缓冲中的 hchan 指针对应对象被误回收；
- `packets=... want=...`：子 channel 缓冲丢失，通常是 channel 元素的写屏障/扫描问题；
- `next link broken`：packet 内部链在 GC 中被截断；
- 卡死/超时：无缓冲 chan 交接逻辑死锁（用例自身问题优先排查）。

# 参数

- `UAF_SEED`：packet data 的 salt（seed%7）与失败信息；
- 无 TORTURE 参数。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_chan_of_chans_fanout/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
