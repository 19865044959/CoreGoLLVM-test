---
id: l17_chan_struct_ptr
dim: L17_stackmap
priority: P1
sources:
  - sonic@v1.13.2: issue_test/issue45_test.go:66（chan raceTestStruct 传递含 *string/*float64 字段的 struct 值）
  - quic-go@v0.50.1: connection_test.go:1699（chan sentPacket 传递含 []byte 的 struct）
tags: [L17, stackmap, channel, struct-by-value, goroutine, interface-boxing, GC, UAF_SEED]
requires: []
runtime: "<10s（16 轮交接）"
---

# 场景

buffered `chan packet` 按值传递含 `*item`、`[]*item`、`[2]*item`、`string`、`[]byte` 的 struct：发送方构造局部 packet 后发送并在发送后 GC，接收方收到副本后 GC，再经 ack 让发送方二次 GC；两侧各自校验自己持有的副本。最后把同一 packet 值装入 `chan any`，GC 后解箱校验，覆盖接口装箱副本的栈/堆扫描。

# 期望行为

1. 每轮发送方在 send 后与 ack 后两次校验 packet 副本全部字段；
2. 接收方在 recv 后与 ack 后两次校验接收副本；
3. 16 轮无死锁、无丢包；
4. `chan any` 中的 packet 经 GC 解箱后字段正确，原 packet 也保持正确；
5. 最后一行 `ALL PASS l17_chan_struct_ptr`，退出码 0。

# 失败判读

- `sender-after-send` / `recv-after-recv` 失败：chan 缓冲或栈副本指针未被扫描；
- 仅 clobberfree 下 data/list 变垃圾：slice 指针字段提前释放；
- 崩溃：chan send/recv 交接点栈映射错误；
- 挂起超时：ack 配对逻辑异常。

# 参数

- `UAF_SEED`：决定 packet 中 list 长度（3..6）；失败信息带种子。
- 无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_chan_struct_ptr/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
