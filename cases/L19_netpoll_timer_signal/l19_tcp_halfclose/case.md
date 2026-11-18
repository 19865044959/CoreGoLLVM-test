---
id: l19_tcp_halfclose
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - nats-server@v2.11.0: server/norace_test.go:1009 监听器 Shutdown 后已接受连接必须收到 EOF/错误而非超时
  - grpc-go@v1.71.1: internal/transport/transport_test.go:2821 Close 必须等 reader 完成，关闭后不得再有读取回调
  - tailscale@v1.80.2: net/memnet/pipe_test.go:33 内存 pipe 的阻塞读写、EOF 与 deadline 唤醒语义
tags: [L19, tcp, half-close, closewrite, fin, eof, netpoll, pointer, gc, UAF_SEED]
requires: [net]
runtime: "<2s"
---

# 场景

模拟 TCP 连接的真实半关闭：客户端发送 24 个含三节点指针链的帧，服务端读完后
`CloseWrite`（只发 FIN，读方向保持开放）。客户端读到 `io.EOF` 后继续写入 4 个
ACK 帧，服务端在 `CloseWrite` 之后仍能读取它们，最后读到 EOF。帧解码出的指针消息
在传输、半关闭与 GC 之间必须保持一致。

## 期望行为

1. 服务端读完 24 个正向帧，内容与校验和正确；中途显式 GC 后仍正确；
2. 服务端 `CloseWrite` 返回 nil，客户端随后 `Read` 得到 `io.EOF`；
3. 客户端在收到 FIN 后写 4 个 ACK 全部成功，服务端 `CloseWrite` 后仍能读到；
4. 客户端本地 `CloseWrite` 后再次写必须返回错误；
5. 服务端最后 `Read` 返回 EOF，进程退出码 0，最后一行 `ALL PASS l19_tcp_halfclose`。

## 失败判读

- `FAIL: ...`：半关闭方向状态错误（过早整连接关闭、EOF 未传播、ACK 丢失）或指针 payload 被破坏；
- `SIGSEGV` / `fatal error: found bad pointer`：帧解码对象在 GC 后被错误回收；
- 超时：服务端/客户端在半关闭后互相等待，属死锁。

## 参数

- `UAF_SEED`：payload 基址种子（runner 每次重复注入不同值，失败输出携带）；
- 无其他 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_tcp_halfclose/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
