---
id: l02_select_multiplex_close
dim: L02_concurrency
priority: P0
sources:
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:141 关闭时排空队列并拒绝新任务
  - webrtc@v4.1.2: operations_test.go:55 关闭后拒绝新任务、不执行残留操作
tags: [L02, select, channel-close, drain, multiplex, pointer-message, gc]
requires: []
runtime: "<2s"
---

# 场景

模拟 grpc-go `callback_serializer` 与 pion/webrtc operations 队列的“关闭排空”语义：3 个有界 channel，每个由 2 个发送者填充共 96 条指针消息；发送者全部结束后统一 close。单个 mux 循环用 `select` 同时消费 3 条 channel，收到 `ok=false` 后把该 channel 置 nil 继续消费其余 channel，直到全部关闭且排空。每消费 48 条显式分配垃圾并 `runtime.GC()`，随后用仍然持有的最后一条消息指针重校验内容。

# 期望行为

1. 288 条消息全部被 select 消费，每条只出现一次（atomic 位图）；
2. channel 关闭后 select 不 panic、不空转：关闭的 channel 被置 nil 后退出；
3. 每条消息的 `ch/seq/val` 自洽，跨 GC 后仍可读；
4. 最后一行输出 `ALL PASS l02_select_multiplex_close`。

# 失败判读

- `missing item` / `duplicate item`：关闭排空逻辑漏消费或重复消费；
- `item arrived on wrong channel` / `value mismatch`：channel 与消息内容错配；
- `held item ... corrupted by GC`：mux 持有的指针被误回收；
- `panic: send on closed channel` 或 select 死循环：发送者与 close 的协调被破坏。

# 参数

无随机路径；`UAF_SEED` 由 runner 注入但本用例不使用。无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_select_multiplex_close/*.go
GODEBUG=clobberfree=1 /tmp/case
```
