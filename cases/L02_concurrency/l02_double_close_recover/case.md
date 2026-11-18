---
id: l02_double_close_recover
dim: L02_concurrency
priority: P0
sources:
  - grpc-go@v1.71.1: test/goaway_test.go:415 客户端 Close 与服务端 GOAWAY 并发，sync.Once 广播，不得 panic/死锁
  - etcd@v3.5.0: raft/node_test.go:525 事件循环 Stop 幂等与 done 关闭
  - fasthttp@v1.59.0: fasthttputil/pipeconns_test.go:123 / 222 并发读/写/Close 交错，错误必须可判读
tags: [L02, double-close, recover, idempotent-shutdown, sync.Once, nil-channel, closed-send, gc]
requires: []
runtime: "<2s"
---

# 场景

模拟 grpc-go GOAWAY 与 etcd raft Stop 的幂等关闭：`hub` 用 `sync.Once` 保护底层 `data`/`done` channel 的关闭，预填 8 条指针消息，启动 4 个排空者（range data）和 8 个 done 等待者。

收齐全部启动信号并执行 GC 后，16 个 goroutine 并发调用 `Close()`，用 defer+recover 包住并断言无一 panic、底层 close 只执行 1 次。随后对已关闭的 data/done 原始 channel 再次 close、close 一个 nil channel、向已关闭 data 发送，全部必须 panic 且被 recover 判读；正常路径的 recover 必须返回 nil。

# 期望行为

1. 16 路并发幂等 Close 无 panic，`rawCloses==1`；
2. data 中 8 条消息恰好各被一个排空者消费一次，无重复无丢失，payload 跨 GC 不变；
3. 8 个 done 等待者全部被唤醒且各只唤醒一次，token 跨 GC 不变；
4. 已关闭 channel 双重 close、nil channel close、向已关闭 channel 发送均 panic 并可 recover；
5. 非 panic 路径的 recover 返回 nil；
6. 最后一行 `ALL PASS l02_double_close_recover`。

# 失败判读

- `idempotent Close panicked` / `underlying close ran more than once`：Once 保护失效；
- `drained` / `missing drained packets`：关闭后残留消息丢失或重复消费；
- `woken waiters`：close(done) 未正确广播；
- `raw double close` / `nil channel` / `send on closed` 未 panic：recover 判读或 channel 状态机异常；
- `packet corrupted after GC`：排空/等待期间指针失效。

# 参数

- `UAF_SEED`：决定 packet tag 派生值，失败输出带种子；
- 无 TORTURE 参数（P0 固定 8 消息 + 16 closer + 8 waiter）。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_double_close_recover/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
