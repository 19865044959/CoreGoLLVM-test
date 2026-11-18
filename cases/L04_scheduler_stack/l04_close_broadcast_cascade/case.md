---
id: l04_close_broadcast_cascade
dim: L04_scheduler_stack
priority: P0
sources:
  - grpc-go@v1.71.1: internal/grpcsync/event_test.go:65 Fire 幂等，Done channel 只关一次
  - pion/webrtc@v4.1.2: peerconnection_close_test.go:235 50 个 goroutine 并发 GracefulClose，不重复关闭
tags: [L04, close, broadcast, sync.Once, channel, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 gRPC Event/Done 广播与 WebRTC 并发关闭的级联 close：主协程关闭 root 后，4 个 relay 被唤醒，各自用 `sync.Once` 幂等关闭自己的 child（重复请求 3 次），再广播唤醒 32 个停在 child 接收上的 waiter。waiter 在等待前已把私有 3 节点链放到栈上；主协程确认全部 waiter 已发出就绪信号后显式 GC，再关闭 root。唤醒后验证链、验证 close 后 channel 永久就绪。

# 期望行为

1. 每个 waiter 恰好被唤醒一次，结果 id 无重复、无缺失；
2. close 广播后再次接收 child 不阻塞，且重复 close 不 panic；
3. 全部 4 个 child 在级联结束后处于已关闭状态；
4. waiter 栈上的私有链在 GC 与唤醒后保持 `b, b+1, b+2`；
5. 进程退出码为 0，最后一行输出 `ALL PASS l04_close_broadcast_cascade`。

# 失败判读

- `woke ... times`：广播丢失唤醒或 waiter 重复退出；
- panic `close of closed channel`：`sync.Once` 未生效；
- `child not closed`：relay 唤醒链断裂；
- `chain corrupted`：blocked sudog/栈上指针在 close 唤醒后损坏；
- 超时：root 未关闭或 waiter 未被广播到。

# 参数

- `UAF_SEED`：链值种子；
- 固定 4 relay × 8 waiter，无其他放大参数。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_close_broadcast_cascade/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
