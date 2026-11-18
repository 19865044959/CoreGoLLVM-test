---
id: l18_old_chan_composite_ring
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - badger@v4.5.0: db_test.go:1246（TestGetSetRace：无缓冲 chan 交接 key，读侧值拷贝避免读到复用内存）
  - nats-server@v2.11.0: server/client_test.go:1304（TestUnsubRace：投递 goroutine 与退订并发，退订置 tombstone）
tags: [L18, write-barrier, channel, old-chan, buffered, unbuffered, ring-buffer, composite-value]
requires: []
runtime: "<5s"
---

# 场景

老 hub 持有老缓冲 chan（cap=64）、老无缓冲 chan 和老 ring slice（64 项），全部先经两轮 GC 晋升。

1. 阶段一：noinline 生产 64 个 `cEnv`（内含年轻 `*cPkt`、string、mode 字段）放入老缓冲 chan，跨两轮 GC 后由 noinline 消费者取出写入老 ring slice；
2. 阶段二：goroutine 在老无缓冲 chan 上逐个生产 96 个年轻 packet，主协程 select 接收并写入老 ring，每 16 条穿插一次 GC，发送端随后 close；
3. 最终校验 ring 中最近 64 条按环形顺序为 seq 1032..1095，再 GC 后复查最旧保留项。

# 期望行为

1. 缓冲 chan 中的 64 个组合值跨两轮 GC 后内容完整（seq/tag/节点/data），drain 后 chan 为空；
2. 无缓冲 chan 交付的 96 个 packet 顺序正确，close 语义正确，无多余消息；
3. 老 ring slice（老底层数组）循环覆写后，最近 64 条内容与环形顺序正确；
4. 接收过程中穿插 GC 不丢失已入 ring 的对象；
5. 退出码 0，最后一行 `ALL PASS l18_old_chan_composite_ring`。

# 失败判读

- `buffered packet N corrupted`：chan 缓冲写入/读取路径漏写屏障，或缓冲对象被提前回收；
- `ring slot K expected seq S corrupted`：老 ring slice 元素写年轻指针漏屏障，或 close/接收顺序错误；
- `direct channel closed before all packets received` / `receive timeout`：chan 生命周期或生产者异常，非 GC 问题优先；
- clobberfree 下 data 变 0xde、efence 下 SIGSEGV：确认 UAF。

# 参数

无随机路径；`UAF_SEED` 不影响结果。固定 64 条缓冲消息 + 96 条无缓冲消息 + 64 项 ring。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_old_chan_composite_ring/main.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
