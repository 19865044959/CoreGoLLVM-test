---
id: l18_goroutine_stack_handoff
dim: L18_gc_lifecycle_wb
priority: P2
sources:
  - quic-go@v0.50.1: streams_map_outgoing_test.go:110（CloseWithError 时向全部 stream 广播关闭，指针经 channel 跨 goroutine 交接）
  - nats-server@v2.11.0: server/ipqueue_test.go:253（TestIPQueueMultiProducers：通知 channel + 批量 pop + recycle）
tags: [L18, goroutine-stack, channel-handoff, stack-growth, noinline, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 4 轮；TORTURE_ROUNDS 上限 16）"
---

# 场景

先创建无缓冲输入/结果 chan 并双 GC 晋升。每轮 noinline `ghSendOnce` 构造
48 节点年轻链表后送入老 chan，函数返回后主协程不再持有链表；worker goroutine
从老 chan 接收唯一引用，把它放在 320 层深递归的栈帧上（每层带 512B 数组并
调用 noinline `ghTouch` 强制真实栈帧），触发 morestack 栈复制后在栈底执行
双 GC，再遍历链表校验 id 与逐字节 payload 并回传校验和。

与既有 channel 用例不同，本用例把唯一根的扫描路径放在“goroutine 栈 + 栈增长
复制”上，验证 morestack 复制期间的指针栈映射与老 chan 发送写屏障。

## 期望行为

1. 每轮 worker 在深递归栈帧上跨 GC 后仍能完整遍历 48 节点链表；
2. 所有节点 id 连续、payload 逐字节正确，校验和与主协程计算一致；
3. 栈增长（morestack）不丢失被复制的栈指针；
4. 所有轮次结束后 worker 正常退出，无死锁/泄漏；
5. 失败信息带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_goroutine_stack_handoff`。

## 失败判读

- `worker stack copy lost list`：栈帧中的链表指针未被扫描或被 morestack 丢弃；
- `checksum=...`：链表内容被破坏或节点被提前回收；
- 卡死/超时：chan 交接死锁（先排除用例逻辑）；
- 数据损坏在 clobberfree 下更明显，可结合 `GODEBUG=clobberfree=1` 复现。

## 参数

- `UAF_SEED`：节点数据模式与失败信息；
- `TORTURE_ROUNDS`：轮数（默认 4，上限 16）。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_goroutine_stack_handoff/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=12 UAF_SEED=3 /tmp/case
```
