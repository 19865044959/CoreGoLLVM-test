---
id: l19_netpipe_multi_writer_partial
dim: L19_netpoll_timer_signal
priority: P2
sources:
  - etcd@v3.5.0: server/etcdserver/api/rafthttp/stream_test.go:268 stream writer/reader 经管道互发消息
  - nats-server@v2.11.0: test/norace_test.go:295 多节点集群用内存 transport（chan/net.Pipe）模拟
  - tailscale@v1.80.2: net/memnet/pipe_test.go:33 内存 pipe 的阻塞读写与 deadline 唤醒语义
tags: [L19, net-pipe, multi-writer, partial-read, reassembly, netpoll, blocking-io, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: [net]
runtime: "<4s（默认 2 轮 4 writer × 8 帧 × 1KiB；TORTURE_ROUNDS 放大轮数）"
---

# 场景

模拟多路流共用同一同步管道：4 个写 goroutine 各并发写 8 条 1KiB 帧，读端只按
37 字节小块读取并重组完整帧，因此每个写者的 `Write` 都要跨越多次 `Read`、
持续阻塞在 `net.Pipe` 的同步点。读端在中点停顿并显式 GC，此时未完成写者都
在 Write 上；随后继续重组剩余帧，校验每个写者序号严格递增、无丢失/串帧，
帧内 magic/base/pattern/checksum 与三节点指针链在 GC 后完好。

# 期望行为

1. 每轮 32 帧全部到达，每写者恰好 8 帧且序号 0..7 严格递增；
2. 每帧 1KiB 的 header/pattern/checksum 与指针链全部正确，无串帧；
3. 读端按 37 字节分片读取，重组逻辑不得依赖单次 Read 返回整帧；
4. 中点停顿 GC 期间写者保持阻塞，GC 后其栈上指针链不被破坏；
5. 默认 2 轮通过；`TORTURE_ROUNDS` 放大后每轮均通过；
6. 末行 `ALL PASS l19_netpipe_multi_writer_partial`，失败退出码为 1。

# 失败判读

- `writer ... n=... err=...`：同步管道写被提前唤醒/关闭；
- `seq=... want=...`：分片重组错位、帧串序或丢失；
- `bad pattern/checksum`：分片拼接或帧内容被破坏；
- `payload ... corrupted after GC`：写者/读者栈或解码对象在 GC 后失效；
- 超时：写者与读者互相等待，属 netpoll 同步点唤醒缺陷。

# 参数

- `UAF_SEED`：每轮 payload 基址种子；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–20；每轮固定 4 writer × 8 帧；
- 读端固定 37 字节分片；10s read deadline 仅防死锁。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_netpipe_multi_writer_partial/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=10 GOGC=1 GOMAXPROCS=1 GODEBUG=clobberfree=1 /tmp/case
```
