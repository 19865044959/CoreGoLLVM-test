---
id: l17_chan_struct_token_ring
dim: L17_stackmap
priority: P2
sources:
  - nats-server@v2.11.0: server/gateway_test.go:7361（ch := make(chan struct{}) 配合 goroutine 完成信号）
  - nats-server@v2.11.0: server/jetstream_cluster_2_test.go:4967（ch/doneCh 两个 chan struct{}，close 广播退出）
tags: [L17, stackmap, chan-struct, token-ring, goroutine-stack, close-broadcast, TORTURE_ROUNDS, GC, UAF_SEED]
requires: []
runtime: "<10s（默认 16 圈 × 4 worker；TORTURE_ROUNDS 放大圈数，上限 2048）"
---

# 场景

4 个 worker 用无缓冲 `chan struct{}` 组成令牌环：main 向 `turn[0]` 发送首个信号，持令牌的 worker 校验自己的 6 节点指针链、执行两轮 GC、把链上元素 append 到本地 `seen`，再向后继发送令牌。全局第 `4×laps` 个令牌到达时 `close(stop)` 广播退出。结果按 worker 索引收集，main 核对每个 worker 的圈数、链 checksum 与 seen checksum。

`UAF_SEED` 决定每个 worker 的链 base；`TORTURE_ROUNDS` 放大圈数。

# 期望行为

1. 每个 worker 恰好处理 `laps` 圈，总令牌数为 `4×laps`；
2. 每圈在 GC 前后校验 6 节点链，chainSum 等于 `laps×perChain`；
3. 每圈 append 到 seen 的元素在 GC 后全部有效，seenSum 与模型一致；
4. 被阻塞在 `<-turn[i]` 的 worker 由 close(stop) 唤醒，无死锁；
5. 所有 worker 报错为空，main 在最终 GC 后再核对汇总；
6. 退出码 0，最后一行 `ALL PASS l17_chan_struct_token_ring`。

# 失败判读

- `processed tokens`：令牌环丢失/重复，或 close 时机错误；
- `laps=... want=...`：某 worker 未被唤醒或提前退出；
- `chain across GC` / `seen across GC`：goroutine 栈槽或捕获切片未被扫描；
- `chainSum` / `seenSum`：GC 后指针链内容变化；
- 超时：令牌未继续传递，或 stop 未广播。

# 参数

- `UAF_SEED`：每轮链 base 种子，失败输出带该值；
- `TORTURE_ROUNDS=N`：默认 16 圈，上限 2048；每圈 4 个 worker 各处理一次令牌。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_chan_struct_token_ring/*.go
UAF_SEED=1 TORTURE_ROUNDS=64 GODEBUG=clobberfree=1 /tmp/case
```
