---
id: l19_netpipe_payload
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - etcd@v3.5.0: server/etcdserver/api/rafthttp/stream_test.go:268 stream writer/reader 经管道互发消息，writer/reader 各一 goroutine
  - nats-server@v2.11.0: server/client_test.go:1945 net.Pipe + deadline 制造阻塞，关闭连接后检查注册表归零
tags: [L19, net-pipe, netpoll, goroutine, pointer, gc, close, UAF_SEED]
requires: [net]
runtime: "<2s"
---

# 场景

模拟 etcd rafthttp 的流式 writer/reader：`net.Pipe` 两端各一个 goroutine，发送端把含三节点指针链的消息编码为 48 字节帧（magic/id/base/name），接收端解码后通过 channel 交给主协程。主协程在收到每个解码对象后显式 GC 并校验指针内容，再放行接收端回 ack、发送端继续下一帧。共 64 帧。

结束后另建一对 `net.Pipe`，让一个 goroutine 阻塞在 `Read` 上，对端 `Close()`，验证阻塞读被错误唤醒而不是永久挂起。

# 期望行为

1. 64 帧全部送达并按序解码，无丢失/重复；
2. 每帧 payload 在主协程 GC 后 `id/base/name/指针链` 完整；发送端在本端消息上也做一次 GC 后校验；
3. reader/writer 均无错误并正常退出；
4. `Close` 能唤醒阻塞的 `Read`（返回非 nil 错误）；
5. 最后一行输出 `ALL PASS l19_netpipe_payload`。

# 失败判读

- `reader payload ... corrupted after GC`：管道读端解码对象被误回收；
- `writer payload ... corrupted`：发送端闭包/栈对象损坏；
- `read/decode/write` 错误：帧格式或同步协议被破坏；
- `Close did not unblock blocked Read`：关闭语义异常，可能死锁；
- 超时：握手顺序错误。

# 参数

- `UAF_SEED`：base 值种子（默认 1），runner 每次重复注入不同值；
- 固定 64 帧、48 字节定长帧；所有等待使用 5s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_netpipe_payload/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
