---
id: l19_os_pipe_bulk
dim: L19_netpoll_timer_signal
priority: P2
sources:
  - caddy@v2.10.0: caddytest/integration/stream_test.go:206 大响应经 chunked 回传后逐字节比对
  - nats-server@v2.11.0: server/norace_test.go:62 500 条 1MB 消息打向同一订阅者，全部送达
  - caddy@v2.10.0: caddytest/integration/stream_test.go:22 io.Pipe 写 body、流式连接关闭不卡死
tags: [L19, os-pipe, blocking-io, bulk-data, netpoll, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 1 轮 1MiB；TORTURE_ROUNDS 放大轮数）"
---

# 场景

模拟 caddy 大响应流式回传与 NATS 大消息投递：每轮通过 `os.Pipe` 写入 256 个 4128 字节块（约 1MiB），每块含 magic/索引/总数/base/seed 头与确定性 pattern。读端读到首块后阻塞在 gate 上，写端继续填充内核管道直至阻塞，主协程此时显式 `runtime.GC()`；放行后读端逐块校验 pattern，并在每 64 块持有指针链时再次 GC。写端关闭后读端必须读到 EOF。

# 期望行为

1. 每轮 256 块全部按序读取，无丢失/重复；
2. 每块头部字段与 4096 字节 pattern 逐字节正确，`bad == 0`；
3. gate 阻塞期间 GC 后，读端持有的三节点指针链完整；
4. 写端 Close 后读端额外一次 Read 返回 `io.EOF`；
5. 默认 1 轮通过；`TORTURE_ROUNDS` 放大轮数后每轮均通过；
6. 最后一行输出 `ALL PASS l19_os_pipe_bulk`。

# 失败判读

- `reader chunks=...`：块丢失或读端提前返回；
- `bad chunks=...`：管道传输内容损坏或 pattern 计算不一致；
- `writer/reader error`：阻塞读写或 Close/EOF 语义异常；
- `payload chain corrupted`：读端持有对象在 GC 后损坏；
- 超时：管道缓冲填满后协调死锁。

# 参数

- `UAF_SEED`：pattern 与 base 种子（默认 1）；
- `TORTURE_ROUNDS`：轮数，默认 1，范围 1–20；每轮固定 256 块 × 4128 字节；
- 所有等待使用 5s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_os_pipe_bulk/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 TORTURE_ROUNDS=10 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
