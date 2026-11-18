---
id: l02_chan_interface_message
dim: L02_concurrency
priority: P1
sources:
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:37 100 goroutine 调度 + 单消费者 FIFO（回调值队列）
  - grpc-go@v1.71.1: test/clientconn_state_transition_test.go:58 观察者接口回调与状态分派
  - etcd@v3.5.0: pkg/pbutil/pbutil_test.go:23 接口 Marshaler/Unmarshaler 的 Must 封装与 type switch 判读
tags: [L02, channel-of-interface, type-switch, nil-interface, dynamic-type, pointer-payload, gc]
requires: []
runtime: "<3s"
---

# 场景

模拟 grpc-go 回调/状态观察者队列的接口值传递：3 个生产者各发送 100 个 `event` 接口值，偶数序号为 `*addEvent`、奇数为 `*delEvent`，两者都实现 `Kind/Prod/Seq/Valid/Blob` 方法集；channel 预置一个 nil 接口值。

单消费者从 cap=12 的 channel range 读取：先判 `e == nil`，再用 type switch 校验动态类型与 Kind 一致；按生产者维护期望序号发现乱序/重复，累加按类型区分的 seed 派生校验和。每 24 条显式 GC，并跨 GC 重校验接口值与底层 `*blob`。

# 期望行为

1. 恰好 300 条事件（150 add + 150 del）加 1 个 nil 接口，每生产者序号 0..99 严格递增且无重复；
2. 每个事件 `Valid()` 为真，动态类型与 `Kind()` 一致，payload 的 tag 与 seed/prod/seq 派生值一致；
3. 跨 GC 持有的接口值动态类型、方法调用与底层 blob 均不变；
4. `event(nil)` 为真 nil；`(*addEvent)(nil)` 装入接口后不等于 nil；`int` 不实现 event；
5. 最后一行 `ALL PASS l02_chan_interface_message`。

# 失败判读

- `event out of order or duplicate`：接口消息丢序/重复，channel 或调度逻辑错误；
- `unexpected dynamic event type`：itab/类型断言错误或写入错误类型；
- `held interface event/payload corrupted after GC`：接口 data 指针或 blob 跨 GC 失效；
- add/del 计数或校验和不符：漏消息或动态分派错。

# 参数

- `UAF_SEED`：决定事件 tag 派生值，失败输出带种子；
- 无 TORTURE 参数（P1 固定 3×100 条）。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_chan_interface_message/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
