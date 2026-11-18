---
id: l02_flow_credits_backpressure
dim: L02_concurrency
priority: P2
sources:
  - grpc-go@v1.71.1: internal/transport/transport_test.go:1606 5 条并发流的流级/连接级流控窗口收支恒等
  - kitex@v0.13.0: pkg/remote/trans/nphttp2/grpc/flowcontrol_test.go:27 writeQuota.get 配额不足阻塞、replenish 唤醒
  - quic-go@v0.50.1: send_stream_test.go:985 发送窗口与 ACK 前重传/窗口释放
tags: [L02, credits, flow-control, backpressure, window-update, blocked-detection, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 64 条消息；TORTURE_ROUNDS 放大消息数，上限 16 倍）"
---

# 场景

模拟 grpc/kitex 的 credit 流控：发送方每发一条消息必须先取得 1 个 credit，接收方每消费满一个窗口（4 条）就批量归还 4 个 credit。窗口耗尽后发送方阻塞在 credits channel 上，形成真实背压；接收方不会无限预取。

为避免用墙钟判断“发送方是否阻塞”，发送方在每个窗口边界设置 `needCredit` 标志并递增 blocked，然后执行阻塞接收；接收方消费完该窗口后等待该标志出现，再归还 credit。这样 blocked 次数可确定地等于 `total/window - 1`。

# 期望行为

1. 接收方严格按 0..N-1 顺序收到全部消息，无丢失、无重复；
2. 发送方在 15 个窗口边界各阻塞一次，`blocked == total/window - 1`；
3. 归还 credit 总数等于消息总数，结束时残留窗口为 4（表示窗口已补满）；
4. 消息 payload 在发送/接收两侧的 GC 前后均完整；
5. 最后一行 `ALL PASS l02_flow_credits_backpressure`。

# 失败判读

- `receiver seq`：credit 放行导致乱序或丢失；
- `blocked != expected`：窗口归还时机错误，背压未真正生效或发送方未阻塞；
- `returned != total` / `residual credits`：credit 记账不守恒，出现超发或泄漏；
- 超时：接收方等待 needCredit 但发送方未进入阻塞（或反之）；
- `payload corrupted`：消息指针跨 channel/GC 失效。

# 参数

- `UAF_SEED`：决定消息 payload 派生值；
- `TORTURE_ROUNDS`：放大消息总数（默认 64，最大 16 倍；总数保持 window 的整数倍）。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_flow_credits_backpressure/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=8 UAF_SEED=2 /tmp/case
```
