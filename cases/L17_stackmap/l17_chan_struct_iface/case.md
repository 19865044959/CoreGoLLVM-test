---
id: l17_chan_struct_iface
dim: L17_stackmap
priority: P1
sources:
  - nats-server@v2.11.0: server/ipqueue_test.go:253（三生产者/一消费者 + 通知 channel + 切片池）
  - fasthttp@v1.59.0: http_test.go:456（10 goroutine 并发 SwapBody 20 轮，交换 body 缓冲所有权）
tags: [L17, stackmap, channel, struct-by-value, any, func-field, map, close-range, GC, UAF_SEED]
requires: []
runtime: "<10s（默认 3 轮 × 10–14 条）"
---

# 场景

buffered `chan envelope` 按值传递同时含 `*item`、`[]*item`、`string`、`any`（指针或含指针 struct 值）、`func() *item`、`map[string]*item` 的聚合。发送侧在 noinline `deliver` 的栈帧里持有 envelope 副本，send 前后各 GC；入队后发送方把本地副本的 head/list/fn/map 全部置 nil 并改写 tag，证明接收方拿到的是独立副本。消费侧 `for range` 逐条接收，每条之间 GC，并调用 fn、查 map、对 any 做 type switch。channel 关闭后消费结束。

## 期望行为

1. 每轮恰好接收 total 条 envelope，id 连续；
2. 每个接收副本的 head/next、3 个 list 元素、map 两个 key、fn 返回值与 any 载荷在 GC 后全部正确；
3. `deliver` 的 defer 中 envelope 副本（send 后）仍完好；
4. 发送方改写本地副本不影响接收副本（tag/head/fn 等按接收值校验）；
5. channel close 后 range 正常结束，无死锁；
6. 最后一行 `ALL PASS l17_chan_struct_iface`，退出码 0。

## 失败判读

- `recv: envelope N corrupted`：chan 缓冲元素或接收栈副本的指针位图漏标；
- `deliver defer: envelope copy corrupted`：发送侧栈副本在 send 后 GC 时未被扫描；
- `sender id corrupted`：按值 send 的副本被错误改写（非 GC 问题）；
- 仅 clobberfree 下失败：chan 缓冲引用的对象被提前回收；
- 挂起超时：生产/消费配对异常。

## 参数

- `UAF_SEED`：决定每轮 base 与 total（10–14 条）；
- 无 TORTURE 参数，3 轮固定。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_chan_struct_iface/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
