---
id: l17_select_struct_iface_notify
dim: L17_stackmap
priority: P2
sources:
  - nats-server@v2.11.0: server/norace_test.go:680（缓冲 chan struct{} + select send/default 非阻塞通知）
  - nats-server@v2.11.0: server/jetstream_consumer_test.go:124（done := make(chan struct{}) 作为退出通知）
tags: [L17, stackmap, select, chan-struct, interface-variadic, nested-select, nil-channel, TORTURE_ROUNDS, GC, UAF_SEED]
requires: []
runtime: "<10s（默认 64 轮；TORTURE_ROUNDS 放大轮数，上限 4096）"
---

# 场景

每轮按 `r%4` 让 select 只有一条就绪路径（其余通道置 nil）：tick 接收、closed 广播接收、ack 发送、全部 nil 走 default。closed 路径内再执行一次嵌套 select 向 ack 非阻塞发送。case body 经接口 `Notify(kind, vals ...any)` 转发含指针的 []any；notify 内与 defer 快照内各执行两轮 GC 并重扫变参切片。每轮还校验 tick 被消费、ack 令牌存在、closed 已关闭。

`UAF_SEED` 决定每轮 base；`TORTURE_ROUNDS` 放大轮数。

# 期望行为

1. select 实际路径等于 `r%4`，四条路径均被执行；
2. 路径 1 的嵌套 select 必定发送成功（nested=sent），路径 2 的 ack 令牌可被取出；
3. notify 返回 `len(kind)*1000 + scanArgs(vals)`，GC 前后变参 sum 不变；
4. defer 快照在 select 之后 GC，snapshot 与调用前 vals sum 一致；
5. 每条路径的命中计数等于 `rounds/4` 的分布；
6. 退出码 0，最后一行 `ALL PASS l17_select_struct_iface_notify`。

# 失败判读

- `select took path`：就绪集构造错误或 nil 通道未禁用；
- `nested select` / `nested ack`：嵌套 select 发送语义异常；
- `notify sum` / `defer snapshot`：接口变参切片或 case 内数据跨 GC 损坏；
- `tick not consumed` / `send-case token missing`：case body 的通道操作错误；
- 超时：通知通道预置/排空逻辑导致阻塞。

# 参数

- `UAF_SEED`：每轮 base 种子，失败输出带该值；
- `TORTURE_ROUNDS=N`：默认 64 轮，上限 4096。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_select_struct_iface_notify/*.go
UAF_SEED=1 TORTURE_ROUNDS=256 GODEBUG=clobberfree=1 /tmp/case
```
