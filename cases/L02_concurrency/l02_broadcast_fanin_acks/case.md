---
id: l02_broadcast_fanin_acks
dim: L02_concurrency
priority: P1
sources:
  - nats-server@v2.11.0: test/fanout_test.go:34 大规模订阅扇出，每条消息在每个订阅按序到达
  - etcd@v3.5.0: server/mvcc/watchable_store_test.go:532 256 订阅者、缓冲 1、慢订阅者触发淘汰与通知
  - webrtc@v4.1.2: datachannel_go_test.go:68 512 条消息单 channel 保序
tags: [L02, fan-out, fan-in, broadcast, ack, bounded-inbox, backpressure, ordering, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟可靠的 fan-out + fan-in 组合：dispatcher 将每条事件广播到 3 个容量 1 的订阅信箱（阻塞发送形成背压），订阅者按序处理后再把 ack fan-in 到共享 channel；其中一个订阅者处理到一半暂停，验证慢订阅者不会丢事件。对应 nats-server 扇出、etcd watch 慢订阅者与 pion datachannel 保序。

## 期望行为

1. 进程退出码为 0，最后一行 `ALL PASS l02_broadcast_fanin_acks`；
2. 每个订阅者按 0..63 顺序收到全部 64 条事件，恰好一次，载荷与 tag 正确；
3. ack 总数 = 3×64 = 192，每个订阅各 64，ack 序号连续；
4. 暂停/恢复期间 dispatcher 可被反压但不得丢事件或死锁；
5. GC 前后事件与 ack 校验和一致。

## 失败判读

- `out-of-order or corrupted event`：广播复用了可变事件指针，或订阅者未按 FIFO 处理；
- `ack order broken`：ack fan-in 中同一订阅的序号缺口/重复；
- `subscriber N got=...`：close 级联过早或 slow consumer 被丢弃；
- 超时：暂停订阅者未被 resume，或 dispatcher 在满信箱上永久阻塞。

## 参数

- `UAF_SEED`：事件载荷种子，失败信息带种子；
- 固定 3 订阅 × 64 事件、信箱容量 1，暂停点在第 32 条，无墙钟阈值。

## 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_broadcast_fanin_acks/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
