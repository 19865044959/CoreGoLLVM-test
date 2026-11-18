---
id: l18_old_chan_recv_young
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - badger@v4.5.0: db_test.go:1246（写协程经无缓冲 chan 交 key，读协程立即读取并做值拷贝）
tags: [L18, write-barrier, channel, producer-consumer, old-chan, gc]
requires: []
runtime: "<3s"
---

# 场景

两个 hub 的 channel 与接收容器先晋升为老对象：

1. 阶段一：老缓冲 chan（cap=64）中放入 64 个由 noinline `produce` 分配的年轻 packet，此时对象只被老 chan 引用；跨两轮 GC 后由 noinline `recvAllBuffered` 接收到老 hub 的 slice，再 GC 校验；
2. 阶段二：老无缓冲 chan 上由 goroutine 调用 noinline `produce` 逐个分配并发送，主协程 noinline `recvOne` 接收并追加到老 hub 的第二个 slice，接收过程中每 16 条穿插一次 GC；关闭后再确认无多余消息。

# 期望行为

1. 缓冲 chan 中的 64 个年轻对象跨 GC 后内容完整（seq/tag/24 字节模式）；
2. 无缓冲 chan 接收的 64 个对象在多次 GC 后内容完整且顺序正确；
3. 接收容器（老 slice）长度正确，chan 关闭语义正确；
4. 退出码 0，最后一行 `ALL PASS l18_old_chan_recv_young`。

# 失败判读

- `packet N corrupted`：chan 缓冲/接收路径写年轻指针漏写屏障，或接收后追加到老 slice 时漏屏障；
- `unexpected channel close before all packets received`：生产者/chan 生命周期被提前破坏；
- `channel delivered packets after expected close`：close 语义错误；
- clobberfree 下 data 变 0xde、efence 下 SIGSEGV：确认 UAF。

# 参数

无随机路径；两阶段各 64 条消息。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_old_chan_recv_young/main.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
