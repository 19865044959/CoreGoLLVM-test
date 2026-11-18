---
id: l03_chan_poll_loop
dim: L03_async_preempt
priority: P1
sources:
  - nats-server@v2.11.0: server/norace_test.go:11455 有界队列 + 非阻塞/轮询发送 + 运行时开关切换
  - quic-go@v0.50.1: connection_test.go:898 未处理包达上限后改用 select/default 丢弃，消费者不得阻塞
tags: [L03, async-preempt, non-blocking-channel, select-default, polling-loop, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟非阻塞队列消费者：生产者在 unbuffered channel 上按 `UAF_SEED` 生成的排列发送 4096 条带指针的消息，消费者用 `select { case <-ch: ... default: ... }` 轮询。`default` 分支持有最近收到的节点指针做 8 次无调用解引用，并把轮询计数与指针值计入校验；当轮询达到 128 次后打开 gate 放行生产者。每收到 512 条消息显式 `runtime.GC()`，另有一个 GC pump 并发执行 8 轮 GC。

# 期望行为

1. 进程退出码为 0；
2. 最后一行输出 `ALL PASS l03_chan_poll_loop`；
3. 4096 条消息全部收到、无重复，按 id 逐条标记且 payload 完好；
4. 收到消息的校验和等于预构造总和；
5. `default` 分支确实被执行（`polls >= 128`），且该分支跨显式 GC 解引用的最近指针仍有效；
6. 主线程每 512 条消息的显式 GC 次数为 8，并发 GC pump 至少完成一轮；
7. 在 base/clobberfree/efence/k8s 等 profile 下输出与校验结果一致。

## 失败判读

- `FAIL: all messages received`：gate 或 channel 关闭顺序导致丢消息，检查轮询回退路径；
- `FAIL: duplicate messages`：排空逻辑或生产者重复发送；
- `FAIL: received checksum`：消息 payload 被 GC 破坏或指针悬空；
- `FAIL: non-blocking default branch exercised`：调度差异导致 gate 放行过早，检查 `polls` 条件；
- `SIGSEGV`：default 分支持有的 `last` 指针未被栈映射正确标记。

## 参数

- `UAF_SEED`：生产者发送排列由该种子派生；失败信息带出种子；
- 无其他放大参数，P1 固定规模。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_chan_poll_loop/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
