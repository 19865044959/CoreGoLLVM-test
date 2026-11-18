---
id: l02_chan_close_drain
dim: L02_concurrency
priority: P0
sources:
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:141 关闭时排空残留任务
  - webrtc@v4.1.2: operations_test.go:55 关闭后拒绝新任务
  - etcd@v3.5.0: raft/node_test.go:525 事件循环 Stop 幂等与 done 关闭
tags: [L02, channel-close, drain, recover, double-close, closed-select, gc]
requires: []
runtime: "<2s"
---

# 场景

模拟 grpc-go / pion/webrtc 关闭队列与 etcd raft node 的 Stop 幂等：预填 12 条指针消息后 close 一个缓冲 channel，range 排空剩余项并校验 FIFO 顺序与 payload 在 GC 后仍有效；随后验证已关闭 channel 在 `select` 中永远就绪（不会落到 default）。另起一个独立 channel，由 8 个 goroutine 并发 `close`，用 defer+recover 判定“恰好一个成功、其余可判读地 panic”，并验证再次 close 与向已关闭 channel 发送都能被 recover 捕获。

# 期望行为

1. close 后仍能读出全部 12 条预填消息，顺序为 0..11，payload 跨 GC 内容不变；
2. 已关闭 channel 的 `<-ch` 在 select 中始终走 receive 分支，返回 `ok=false`、`v=nil`；
3. 8 路并发 close 恰好 1 个成功、7 个 recover 到 panic；
4. 对已关闭 channel 再次 close 与执行 send 均 panic 且可被 recover 判读；
5. 最后一行 `ALL PASS l02_chan_close_drain`。

# 失败判读

- `drain order mismatch` / `drained payload corrupted`：close 后残留消息丢失或 payload 被 GC 破坏；
- `closed channel receive not ready in select`：关闭语义被破坏；
- `close winners` / `close panics`：并发 close 的 recover 判读不符合语言语义；
- `sending on a closed channel must panic` 失败：close/send 状态机异常。

# 参数

无随机路径；`UAF_SEED` 由 runner 注入但本用例不使用。无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_chan_close_drain/*.go
GODEBUG=clobberfree=1 /tmp/case
```
