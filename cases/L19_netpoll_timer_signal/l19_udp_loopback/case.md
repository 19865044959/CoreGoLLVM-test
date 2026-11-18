---
id: l19_udp_loopback
dim: L19_netpoll_timer_signal
priority: P2
sources:
  - quic-go@v0.50.1: transport_test.go:122 net.ListenUDP 回环 + 并发 Close，错误必须可区分
  - quic-go@v0.50.1: integrationtests/self/timeout_test.go:234 idle timeout 到期后连接按时关闭
  - nats-server@v2.11.0: server/norace_test.go:1009 监听器关闭与并发拨号，已接受连接必须收到 EOF/错误
tags: [L19, udp, loopback, netpoll, echo, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: [net]
runtime: "<3s（默认 1 轮 64 报文；TORTURE_ROUNDS 放大轮数）"
---

# 场景

模拟 quic-go 回环 UDP transport 与 NATS 监听器路径：每轮在 `127.0.0.1:0` 启动 UDP echo 服务端，客户端逐个发送 64 个 512 字节定长帧（id/base/seed/checksum/pattern）。服务端解码、校验 checksum 与 pattern，持有解码指针链并回显 base+1；客户端校验回显后持有指针链，每 16 个报文显式 `runtime.GC()` 并复核全部已收报文。服务端每 16 个报文同样 GC 并校验自身链。

# 期望行为

1. 每轮 64 个报文全部完成请求/回显，服务端 `seen == 64`；
2. 服务端与客户端 checksum/pattern 校验全部通过，`bad == 0`；
3. 两端持有的三节点指针链在 GC 后内容完整；
4. 默认 1 轮通过；`TORTURE_ROUNDS` 放大轮数后每轮均通过；
5. 最后一行输出 `ALL PASS l19_udp_loopback`。

# 失败判读

- `udp server seen=...` / `udp client got=...`：报文丢失或回环阻塞；
- `bad=...`：帧内容损坏或 checksum/pattern 不一致；
- `udp read/write ...`：UDP 读写错误（含 watchdog deadline，仅防死锁）；
- `chains corrupted`：回环路径解码对象在 GC 后损坏；
- 超时：服务端未响应或 Close 路径死锁。

# 参数

- `UAF_SEED`：base 与 pattern 种子（默认 1）；
- `TORTURE_ROUNDS`：轮数，默认 1，范围 1–20；每轮固定 64 个 512 字节报文；
- 单次读等待使用 5s deadline，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_udp_loopback/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 TORTURE_ROUNDS=10 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
