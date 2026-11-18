---
id: l19_udp_deadline_churn
dim: L19_netpoll_timer_signal
priority: P2
sources:
  - quic-go@v0.50.1: transport_test.go:122 net.ListenUDP 回环 + 并发 Close，错误必须可区分
  - quic-go@v0.50.1: integrationtests/self/timeout_test.go:234 idle timeout 到期后连接按时关闭
  - etcd@v3.5.0: client/pkg/transport/timeout_dialer_test.go:23 回环 socket 上的阻塞读写与超时语义
tags: [L19, udp, deadline, read-deadline, write-deadline, close, netpoll, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: [net]
runtime: "<5s（默认 2 轮；TORTURE_ROUNDS 放大轮数）"
---

# 场景

模拟 UDP transport 的 deadline 与关闭 churn：每轮先验证已过期读 deadline 立即
返回 timeout，再验证已过期写 deadline 立即失败；清空 deadline 后完成 16 个
512 字节定长数据报的收发；最后让 goroutine 阻塞在 `ReadFromUDP` 上，关闭 socket
必须把它唤醒为 `net.ErrClosed`。数据报解码为含指针链的消息并在 GC 后校验。

## 期望行为

1. 过期读 deadline 的 `ReadFromUDP` 返回 `os.ErrDeadlineExceeded`；
2. 过期写 deadline 的 `Write` 立即返回 timeout，数据报未发出；
3. 清空 deadline 后 16 个数据报全部按序收发，magic/base/pattern/checksum 正确；
4. 阻塞中的 `ReadFromUDP` 在 socket 关闭后返回 `net.ErrClosed`；
5. 每轮 GC 后已持有消息的指针链正确；
6. `TORTURE_ROUNDS=N` 时重复 N 轮；退出码 0，末行 `ALL PASS l19_udp_deadline_churn`。

## 失败判读

- 过期 deadline 未立即返回：UDP fd 未走 netpoll deadline 检查；
- 数据报 decode 失败：过期写实际发出、序号错乱或 payload 被破坏；
- 关闭后阻塞读未唤醒：close 未进入 netpoll 唤醒路径；
- 超时：收发两端互相等待。

## 参数

- `UAF_SEED`：每轮 payload 基址种子；
- `TORTURE_ROUNDS`：轮数，默认 2，上限 50。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_udp_deadline_churn/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=5 GODEBUG=clobberfree=1 /tmp/case
```
