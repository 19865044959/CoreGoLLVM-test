---
id: l19_ticker_select_merge
dim: L19_netpoll_timer_signal
priority: P2
sources:
  - etcd@v3.5.0: server/lease/lessor_test.go:526 1s checkpoint ticker 周期回调，Stop 后停止
  - tailscale@v1.80.2: net/connstats/stats_test.go:50 Ticker 驱动周期 dump，Shutdown 时关闭 ticker
  - quic-go@v0.50.1: connection_test.go:1951 ACK alarm 到期驱动事件循环并继续发包
tags: [L19, ticker, select, event-loop, stop, channel, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 128 事件/轮；TORTURE_ROUNDS 放大轮数）"
---

# 场景

模拟事件循环把周期 ticker、数据投递 channel 与 GC 合流到同一个 `select` 的
路径。生产者与第一个 tick 握手后才投递 128 条含指针链的消息；主协程在
`select` 中交替处理 tick（周期性显式 GC）与事件，保证每条消息恰好处理一次。
处理完毕后 Stop ticker，验证 ticker channel 未被关闭且不再投递。

## 期望行为

1. 128 条事件全部处理且 id 不重复，校验和正确；
2. 至少产生 1 个 tick（生产者等待第一个 tick 后才开始发送）；
3. tick 路径上的 GC 不破坏正在处理的消息指针链；
4. `Ticker.Stop` 后通道不关闭、可以非阻塞读取，且不再有新 tick；
5. 生产者 goroutine 在事件投递完成后退出；
6. 最后一行输出 `ALL PASS l19_ticker_select_merge`，失败退出码为 1。

## 失败判读

- `processed=... want=...`：select 合流丢事件或把 ticker 与事件通道混淆；
- `events closed after ...`：生产者在第一个 tick 前投递或提前关闭通道；
- `ticker delivered a tick after Stop`：Stop 语义或通道关闭逻辑错误；
- `duplicated/corrupted`：消息指针链在 tick 驱动的 GC 下被破坏；
- 超时 10s：select 饥饿或生产者/消费者互相等待。

## 参数

- `UAF_SEED`：随机路径种子，参与每条消息的基值计算；
- `TORTURE_ROUNDS`：轮数（1–20），每轮新建 ticker 与生产者。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_ticker_select_merge/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
