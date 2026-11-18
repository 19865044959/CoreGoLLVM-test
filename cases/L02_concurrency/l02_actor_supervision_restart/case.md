---
id: l02_actor_supervision_restart
dim: L02_concurrency
priority: P1
sources:
  - etcd@v3.5.0: raft/node_test.go:353 node.run 单 goroutine 事件循环，关闭 stop 后退出
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:141 关闭时排空残留任务、新任务被拒绝
  - nats-server@v2.11.0: server/jetstream_test.go:770 Shutdown 与删除 100 个 consumer 并发，不得 panic
tags: [L02, actor, supervision, panic-recover, restart-budget, mailbox, escalation, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 actor 监督树：单 goroutine supervisor 串行处理 mailbox，子 actor 的状态由 supervisor 独占。正常命令累加计数器；poison 命令在处理函数中 panic，supervisor 用 recover 捕获、丢弃崩溃前的部分状态并重建子 actor，重启预算为 2。第三次 panic 后升级为 `stopped`，后续命令不执行但仍返回 stopped 响应，保证发送者不会被阻塞。

命令序列固定：5 正常 → poison → 3 正常 → poison → 2 正常 → poison → 2 正常。通过回复中的 counter/gen/restarts 验证重启后状态归零、旧状态不泄漏、升级后 drop 计数准确。

# 期望行为

1. 第一次 poison 后重启（gen=1、restarts=1），后续首个正常命令 counter 从 1 开始；
2. 第二次 poison 后再次重启（gen=2、restarts=2），旧记录不残留；
3. 第三次 poison 触发 stopped，后续 2 条命令以 stopped 响应被丢弃；
4. handled=10、dropped=2、restarts=2，关闭 mailbox 后 supervisor 退出；
5. 命令 payload 经 mailbox 与 GC 后仍完整；
6. 最后一行 `ALL PASS l02_actor_supervision_restart`。

# 失败判读

- `phaseN reply`：recover/重启后状态未重置，或升级阈值错误；
- `handled` / `dropped`：stopped 后仍在执行命令，或正常命令被误丢；
- `command payload corrupted`：命令指针在跨 goroutine/GC 后失效；
- `supervisor state corrupted by GC`：子 actor 状态持有错误指针；
- 超时：升级后发送者阻塞在 reply 上。

# 参数

- `UAF_SEED`：决定命令 payload 派生值；
- 无 TORTURE 参数（固定 15 条命令、2 次重启预算）。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_actor_supervision_restart/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
