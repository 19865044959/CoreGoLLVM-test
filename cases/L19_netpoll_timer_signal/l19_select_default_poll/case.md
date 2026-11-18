---
id: l19_select_default_poll
dim: L19_netpoll_timer_signal
priority: P0
sources:
  - nats-server@v2.11.0: server/sublist_test.go:1127 兴趣通知用非阻塞 send/select-default 合并事件
  - quic-go@v0.50.1: connection_test.go:898 有界队列达到上限后 select/default 直接丢弃并计数
  - nats-server@v2.11.0: server/norace_test.go:11455 有界 channel + select 非阻塞发送，人工 stall 时不阻塞生产者
tags: [L19, select-default, nonblocking, channel-polling, pointer, gc, deterministic]
requires: []
runtime: "<1s"
---

# 场景

模拟 NATS 订阅兴趣通知与 quic-go 有界队列的非阻塞 `select/default` 路径：

1. 3 个预填充 channel（48/16/8 条消息）用固定 48 轮轮询消费，每轮对每个 channel 做一次 `select`，无数据必须走 `default`；
2. 期望成功接收 72 条、default 72 次（c0 无 default，c1 32 次，c2 40 次），且每个 channel 内消息按 id 严格递增；
3. 3 个全空 channel 固定轮询 64 次，必须全部命中 default；
4. 一个已就绪 channel 与两个空 channel 竞争时，必须选择就绪分支而不是 default。

轮询期间每 12 轮显式 GC，校验已消费消息上的三节点指针链。

# 期望行为

1. `got == 72`、`defaults == 72`、`empty_defaults == 64`；
2. 每 channel 消费条数与 id 顺序严格正确，消费后 channel 为空；
3. 所有 GC 检查点 payload 指针链完整；
4. 最后一行输出 `ALL PASS l19_select_default_poll`。

# 失败判读

- `defaults=...` / `got=...`：select/default 轮询分支计数错误或消息丢失；
- `per-channel counts=...`：轮询顺序/通道归属错误；
- `empty select chose ...`：空 channel 被误判就绪；
- `ready select chose ...`：就绪分支优先级异常；
- `payload corrupted`：消息对象在 GC 后损坏。

# 参数

- `UAF_SEED`：消息值 base 种子（默认 1）；
- 固定 48 轮主轮询、64 次空轮询；无 sleep、无耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_select_default_poll/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
