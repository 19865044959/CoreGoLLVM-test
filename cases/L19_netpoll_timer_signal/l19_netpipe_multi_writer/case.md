---
id: l19_netpipe_multi_writer
dim: L19_netpoll_timer_signal
priority: P2
sources:
  - etcd@v3.5.0: server/etcdserver/api/rafthttp/stream_test.go:268 stream writer/reader 经管道互发消息，writer 未就绪时轮询 writec
  - nats-server@v2.11.0: test/norace_test.go:295 15 节点集群用内存 transport（chan/net.Pipe）模拟
  - tailscale@v1.80.2: net/memnet/pipe_test.go:33 内存 pipe 的阻塞读写与 deadline 唤醒语义
tags: [L19, net-pipe, multi-writer, netpoll, blocking-io, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: [net]
runtime: "<4s（默认 1 轮 4 writer × 12 帧；TORTURE_ROUNDS 放大轮数）"
---

# 场景

模拟多路流共用一个管道 transport：4 个写 goroutine 各自写入 12 个 48 字节帧，
读端逐帧全量读取并校验每个写者的序号严格递增、无丢失/重复/串帧。读端在中点
停顿执行显式 GC，此时所有未完成写者都阻塞在 `net.Pipe` 的 `Write`（netpoll
阻塞路径）上；恢复后剩余帧必须按序到达。

## 期望行为

1. 每轮 48 帧全部到达，每个写者恰好 12 帧且序号 0..11 严格递增；
2. magic/name/base/checksum 与解码出的指针链全部正确；
3. 读端停顿期间写者保持阻塞，GC 后其栈上指针链不被破坏；
4. 最终 GC 后所有已解码帧仍正确；
5. `TORTURE_ROUNDS=N` 时上述断言重复 N 轮；
6. 退出码 0，末行 `ALL PASS l19_netpipe_multi_writer`。

## 失败判读

- `seq=x want=y`：帧串序或丢失，管道唤醒/分段处理异常；
- `held=...`：读端提前退出或帧数不足；
- `writer ... corrupted`：写者/读者栈上指针对象被 GC 错误回收；
- 超时：写者与读者互相等待，属 netpoll 阻塞唤醒缺陷。

## 参数

- `UAF_SEED`：每轮 payload 基址种子；
- `TORTURE_ROUNDS`：轮数，默认 1，上限 50。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_netpipe_multi_writer/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=5 GODEBUG=clobberfree=1 /tmp/case
```
