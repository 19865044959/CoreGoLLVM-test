---
id: l19_netpipe_halfclose
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - grpc-go@v1.71.1: internal/transport/transport_test.go:2821 Close 必须等 reader goroutine 完成，关闭后不得再有读取回调
  - tailscale@v1.80.2: net/memnet/pipe_test.go:33 内存 pipe 的阻塞读写、EOF 与 deadline 唤醒语义
  - nats-server@v2.11.0: server/routes_test.go:2079 route pool 建立过程中并发连接/关闭，半开连接不得泄漏
tags: [L19, net-pipe, half-close, fin, eof, netpoll, pointer, gc, UAF_SEED]
requires: [net]
runtime: "<2s"
---

# 场景

`net.Pipe` 本身没有 `CloseWrite`，用例用应用层 FIN 帧模拟半关闭：发送端写完
64 条数据帧后发送 FIN，从此只读不写；接收端读到 FIN 后仍把汇总 ACK 写回
（写方向保持开放），发送端收到 ACK 后 Close；接收端随后必须读到 `io.EOF`，
再写则得到 `io.ErrClosedPipe`。帧解码出含指针链的消息结构，在传输中穿插 GC。

## 期望行为

1. 64 条数据帧按 id 顺序到达，FIN 恰好出现一次；
2. 收到 FIN 后接收端仍能成功写回 ACK，ACK 中的帧数与校验和正确（半关闭的
   读方向仍开放）；
3. 发送端 Close 后接收端 `Read` 返回 `io.EOF`，不是超时；
4. 接收端在对端关闭后 `Write` 返回错误而不是静默成功；
5. 完全不发 FIN 直接 Close 时，对端同样得到 `io.EOF`；
6. 最后一行输出 `ALL PASS l19_netpipe_halfclose`，失败退出码为 1。

## 失败判读

- `receiver want EOF got ...`：net.Pipe 关闭唤醒路径丢失，或读端被误判为超时；
- `receiver write after peer close succeeded`：半关闭状态机错误；
- 帧乱序/校验和错误：`net.Pipe` 同步读写路径在 GC 下丢数据；
- 超时：两端互相等待（写端等读端、读端等 FIN）。

## 参数

- `UAF_SEED`：随机路径种子，参与帧基值与校验和计算。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_netpipe_halfclose/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
