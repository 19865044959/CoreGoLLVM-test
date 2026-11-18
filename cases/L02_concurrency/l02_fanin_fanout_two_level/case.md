---
id: l02_fanin_fanout_two_level
dim: L02_concurrency
priority: P2
sources:
  - nats-server@v2.11.0: test/fanout_test.go:34 大规模订阅扇出，每条消息在每个订阅按 0..N-1 有序到达
  - etcd@v3.5.0: server/mvcc/watchable_store_test.go:532 256 订阅者、缓冲 1、慢订阅者淘汰
  - nats-server@v2.11.0: server/ipqueue_test.go:253 三生产者/一消费者 + 通知 channel + 切片池
tags: [L02, fan-in, fan-out, two-level, WaitGroup, close-cascade, checksum, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 2×60 项；TORTURE_ROUNDS 放大每源条数）"
---

# 场景

模拟 nats-server 扇出与 etcd watch 分发，构造两级 fan-in/fan-out 拓扑：

1. 2 个 source 并发写入同一个 `raw` channel（fan-in）；
2. 4 个 worker 从 `raw` 分流（fan-out），每个 worker 再按 `id%2` 把任务 fan-out 到自己的 2 个 leaf channel；
3. 8 个 leaf 处理并写入 2 个 collector channel（fan-in）；
4. 2 个 collector 各汇总一个 partial result，再 fan-in 到最终 merger。

每级用独立 WaitGroup 收敛并 close 下游：sources→raw、worker 关闭自己的 leaf channel、leaves→collectors、collectors→results。各阶段周期性显式 GC，并重校验输入/输出指针。

# 期望行为

1. 最终合并 count 恰为 `2×perSource`（默认 120，TORTURE 放大）；
2. 所有 leaf 的 seen 位图全为 1（无丢失、无重复）；
3. 合并校验和等于 `Σ finVal(id)`；每级 val 关系 `mid=src*3+1`、`fin=mid*5+2` 成立；
4. 每级持有的 payload 跨 GC 内容不变，所有 channel 正确关闭且无死锁；
5. 最后一行 `ALL PASS l02_fanin_fanout_two_level`。

# 失败判读

- `merged count` / `missing items`：close 级联过早或某级分流错误导致丢消息；
- `duplicate item at leaf`：fan-out 路由重复或重复投递；
- `worker/leaf/collector received corrupted input`：跨级指针被 GC 破坏或 val 派生错误；
- 超时：某级 channel 未被关闭（close 级联遗漏）。

# 参数

- `UAF_SEED`：决定各级 val 的派生值，失败输出带种子；
- `TORTURE_ROUNDS`：放大每源条数，范围 1..100，默认 1。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_fanin_fanout_two_level/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=20 GODEBUG=clobberfree=1 /tmp/case
```
