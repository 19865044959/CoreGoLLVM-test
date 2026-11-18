---
id: l02_select_fairness
dim: L02_concurrency
priority: P1
sources:
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:105 100 个并发调度者通过 select 等待 done，必须全部收敛
  - quic-go@v0.50.1: connection_test.go:1749 run loop 在发送队列阻塞时重置 timer，避免 select 忙等
  - nats-server@v2.11.0: server/norace_test.go:11455 有界 channel + select/default 非阻塞发送，生产者不被慢消费者拖死
tags: [L02, select, fairness, starvation, closed-channel, nil-channel, default, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

验证 Go `select` 在多路就绪时的公平性与边界语义，全部用固定迭代次数，不使用墙钟阈值：

1. 两个已关闭 channel 与 `default` 同时存在：已关闭 case 永远就绪，`default` 一次都不应被选中；
2. 仅含 nil channel 与 `default`：nil channel 永不就绪，`default` 每次都被选中；
3. 4 路都已关闭（始终就绪）：固定 20000 次 select，每个分支的份额必须落在宽裕区间 `[15%, 35%]`，且每个分支至少被选中一次——检测某一路被长期饿死；
4. 2 路都已关闭：固定 12000 次，任意连续同分支长度必须小于 128，检测伪随机调度是否出现病态长串；
5. send/recv 同时就绪（`sink` 有缓冲空位 + `ready` 有可读 token）：固定 4000 次，两路都必须被覆盖。

循环中每 4096 次分配垃圾并执行 `runtime.GC()`，校验持有的 `*blob` payload。

# 期望行为

1. closed-vs-default 阶段 `default` 计数为 0，两个 closed 分支均被选中；
2. nil channel 阶段 nil 分支计数为 0；
3. 4 路公平阶段每个分支计数在 `[3000,7000]`（20000 的 15%..35%）内；
4. 2 路阶段最大连续同分支长度 `<128`，且两分支计数都大于 0；
5. send/recv 混合阶段两个方向都被选中过；
6. `*blob` payload 在循环内多次显式 GC 后内容不变；
7. 最后一行输出 `ALL PASS l02_select_fairness`，退出码 0。

# 失败判读

- `default selected ... despite ready closed channels`：closed channel 的可就绪语义或 select 实现错误；
- `nil channel case must never be ready`：nil channel 被错误地视为就绪；
- `case N share out of fair window`：某一路长期不被选中，select 随机化/轮转失效；
- `select starvation gap=N want<128`：两路场景出现异常长连续串；
- `select did not cover both send and recv cases`：send 分支永远不被选中，通道状态推进错误；
- `payload corrupted by GC`：循环内持有的指针被破坏。

# 参数

- `UAF_SEED`：决定 payload 数值，不改变迭代次数与判定窗口；
- 迭代次数固定，无 `TORTURE_*`。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_select_fairness/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GOMAXPROCS=1 GODEBUG=clobberfree=1,asyncpreemptoff=1 /tmp/case
```
