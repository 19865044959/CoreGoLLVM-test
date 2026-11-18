---
id: l17_select_struct_notify
dim: L17_stackmap
priority: P2
sources:
  - nats-server@v2.11.0: server/gateway_test.go:765（slowResolver 的 inLookupCh/releaseCh 均为 chan struct{}，select send + 阻塞 recv）
  - nats-server@v2.11.0: server/events_test.go:646（received := make(chan struct{})，select/close 广播）
tags: [L17, stackmap, select, chan-struct, closed-channel, nil-channel, defer-in-case, TORTURE_ROUNDS, GC, UAF_SEED]
requires: []
runtime: "<10s（默认 64 轮；TORTURE_ROUNDS 放大轮数，上限 4096）"
---

# 场景

每轮按 `rnd%4` 控制 select 的唯一就绪路径，全部使用 `chan struct{}`：

1. `tick`（缓冲 1）预置 token → `case <-c0`；
2. `closedChan` 已 close → `case <-c1`；
3. 两个接收 channel 均为 nil → `default`；
4. `sent`（缓冲 1）为空 → `case send <- struct{}{}`。

函数级 defer 先注册，case body 内的 defer 后注册（LIFO 先运行）；两个 defer 都 GC 并校验本轮 `*item` 链。main 在函数返回后再 GC 校验，并核对 case 内 defer 路径、defer 顺序与四条路径计数。

`UAF_SEED` 决定每轮 base；`TORTURE_ROUNDS` 放大轮数。

# 期望行为

1. 每轮 select 走中预期路径（0/1/2/3），所有路径至少被覆盖一次；
2. case-body defer 先于函数级 defer 运行，order 记录恰为 `case:<kind>:<rnd>`、`func:<kind>`；
3. 两个 defer 内 GC 后 5 节点链仍完整，main 再次 GC 后仍一致；
4. 每条路径的命中计数与轮数分布一致；
5. 退出码 0，最后一行 `ALL PASS l17_select_struct_notify`。

# 失败判读

- `select took path` / `nil channel selected`：select 就绪集或 nil 禁用逻辑异常；
- `case*/func defer`：case 局部或命名返回槽未被扫描，defer 内指针链损坏；
- `defer order`：defer LIFO 顺序异常；
- `path counters`：某路径未执行或重复计数；
- 挂起超时：channel 预置/排空逻辑错。

# 参数

- `UAF_SEED`：每轮 base 种子，失败输出带该值；
- `TORTURE_ROUNDS=N`：默认 64 轮，上限 4096。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_select_struct_notify/*.go
UAF_SEED=1 TORTURE_ROUNDS=256 GODEBUG=clobberfree=1 /tmp/case
```
