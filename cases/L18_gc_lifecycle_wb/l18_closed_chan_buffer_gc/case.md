---
id: l18_closed_chan_buffer_gc
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - nats-server@v2.11.0: server/client_test.go:1304（TestUnsubRace：退订 tombstone 与投递并发，关闭后队列仍需可排空）
  - badger@v4.5.0: db_test.go:1246（TestGetSetRace：无缓冲 chan 同步交接后读侧做值校验）
tags: [L18, channel, close, buffered, young-payload, GC, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

先分配容量 32 的缓冲 chan 并双 GC 晋升，再向其中发送 32 条年轻组合消息
（含 `*cbNode`、string、interface）：消息入老 chan 缓冲后再双 GC，随后 close，
再双 GC，最后用 range 排空并逐条校验。另构造已 close 的老无缓冲 chan，校验
接收返回零值与 `ok=false`。排空结果存入全局 sink，最终 GC 后再次复查。

与既有“老 chan 跨 GC 交接”用例不同，这里 close 发生在年轻 payload 已进入
老缓冲之后，验证关闭态 hchan 的缓冲槽仍被当作根扫描。

## 期望行为

1. close 前/后跨多轮 GC，缓冲中的 32 条消息一条不丢、顺序不变；
2. 每条消息的 id/name、节点 payload、interface 动态类型与数据全部正确；
3. range 排空后再次接收返回零值与 `ok=false`；
4. 已 close 的无缓冲老 chan 接收返回零值与 `ok=false`；
5. sink 中的消息在最终 GC 后仍完整；
6. 失败信息带 `seed=<UAF_SEED>`；
7. 退出码 0，最后一行 `ALL PASS l18_closed_chan_buffer_gc`。

## 失败判读

- `drained ... want ...`：close 后缓冲槽未被扫描或 hchan 指针被误回收；
- `msg ... header/node/interface corrupted`：组合值内指针字段生命周期错误；
- `receive after close not zero`：关闭态接收语义异常（先排除用例逻辑）；
- `sink-after-gc`：排空后的全局容器根写入/扫描错误。

## 参数

- `UAF_SEED`：消息名长度、节点数据模式与失败信息；
- 无 TORTURE 参数。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_closed_chan_buffer_gc/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
