---
id: l19_after_timeout_select
dim: L19_netpoll_timer_signal
priority: P0
sources:
  - grpc-go@v1.71.1: internal/idle/idle_test.go:133 空闲超时 timer 到期后回调置状态并通知
  - etcd@v3.5.0: raft/node_test.go:80 select 同时监听 done/ctx/目标 channel，停止后幂等返回
tags: [L19, select, time-after, timeout, pointer, gc]
requires: []
runtime: "<1s"
---

# 场景

模拟 grpc-go idle timer 与 etcd raft 事件循环中的 `select` 超时分支：

1. 超时路径：工作 channel 永远无发送者，`select` 必须命中 `time.After(1ms)`；循环 32 轮，每轮 payload 在 GC 后校验；
2. 就绪路径：worker 在 `select` 中同时监听请求与 5s 超时，主协程先显式 GC，再投递携带指针的请求，必须走请求分支；
3. 已就绪 channel 与永不就绪 channel 竞争，必须选择已就绪者，长超时分支不得命中；
4. 独立 goroutine 的 `select` 走 1ms 超时分支后，主协程 GC 再校验 payload。

# 期望行为

1. 32 轮超时路径全部命中，`timeouts == 32`；
2. 就绪路径拿到请求指针且内容正确，未走超时分支；
3. 竞争场景只选择已就绪分支；
4. 所有 payload 在 GC 后保持 `id/name/三节点指针链` 正确；
5. 最后一行输出 `ALL PASS l19_after_timeout_select`。

# 失败判读

- `unexpected work item`：死 channel 意外就绪；
- `timeout branch chosen`：请求已投递但 select 走了超时；
- `payload ... corrupted after GC`：select/闭包捕获的指针被误回收；
- 超时：worker 未响应或 select 死锁。

# 参数

- `UAF_SEED`：由 runner 注入但本用例不使用（无随机路径）；
- 超时判定只依据 `select` 命中的分支与计数；5s 等待上限仅用于防死锁。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_after_timeout_select/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
