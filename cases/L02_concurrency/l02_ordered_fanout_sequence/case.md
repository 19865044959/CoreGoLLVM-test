---
id: l02_ordered_fanout_sequence
dim: L02_concurrency
priority: P2
sources:
  - nats-server@v2.11.0: test/fanout_test.go:34 每条消息在每个订阅的回调中按 0..499 顺序到达
  - webrtc@v4.1.2: datachannel_go_test.go:68 512 条消息单 channel 保序
  - quic-go@v0.50.1: frame_sorter_test.go:1377 1000 个帧乱序/重复/重叠推送后按 offset 还原
tags: [L02, fan-out, ordering, sequencer, reorder-buffer, shuffle, checksum, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 120 项；TORTURE_ROUNDS 放大项数，上限 8 倍）"
---

# 场景

模拟 nats 有序扇出与 webrtc datachannel 保序：任务索引先按 `UAF_SEED` 确定性洗牌，再轮转分发给 4 个 worker；每个 worker 逆序完成自己的任务，因此完成顺序与输入顺序不同。主 goroutine 作为 sequencer，用 pending map 缓存乱序结果，只按 0..N-1 连续输出。

验证点：洗牌后每个索引仍恰好分发一次；乱序完成必须被缓存（`maxPending > 0`）；重复结果被拒绝；输出顺序与 payload 内容始终一致；最终 GC 后保留的结果仍然完整。

# 期望行为

1. 全部 N 项恰好被执行一次，无重复、无丢失；
2. sequencer 至少缓存过一次乱序结果，且最终 pending 为空；
3. 输出严格为 0..N-1，payload 的 val/tag 与索引一致；
4. 最终 `emitted == N`，GC 后 payload 不变；
5. 最后一行 `ALL PASS l02_ordered_fanout_sequence`。

# 失败判读

- `duplicate result` / `emitted != want`：分发重复或丢失；
- `final order mismatch`：sequencer 提前或错误释放了带空洞的序列；
- `sequencer never buffered`：逆序完成未生效，乱序路径没被覆盖；
- `sequenced payload corrupted`：结果指针跨 channel/GC 后被破坏；
- 超时：worker 或 sequencer 在 channel 关闭时未退出。

# 参数

- `UAF_SEED`：决定任务洗牌顺序与 payload 派生值；
- `TORTURE_ROUNDS`：放大任务数（默认 120，最大 8 倍）。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_ordered_fanout_sequence/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=4 UAF_SEED=2 /tmp/case
```
