---
id: l13_slice_zero_elems
dim: L13_runtime_ds
priority: P2
sources:
  - quic-go@v0.50.1: datagram_queue_test.go:16 `var queued []struct{}` 以零尺寸元素记录待处理队列
  - etcd@v3.5.0: client/pkg/testutil/leak_test.go:40 `for range make([]struct{}, 100)` 零尺寸循环
  - pion/webrtc@v4.1.2: peerconnection_go_test.go:1043 `for range make([]struct{}, trackCount)` 批量零尺寸信号
tags: [L13, slice, zero-size-element, growslice, append, reslice, copy, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s（默认 2 轮；TORTURE_ROUNDS 可放大）"
---

# 场景

模拟 goquic/webrtc/etcd 中用 `[]struct{}` 作为计数/信号集合的路径。零尺寸元素不分配元素存储，但 `growslice`、len/cap、reslice、copy 仍必须正确：

1. 逐个 append 约 70 万～100 万个 `struct{}`，在固定间隔校验 len；
2. 以 4096 为块 append 到预分配容量的 slice；
3. `zs[n/3:]` 与再切片，校验派生长度；
4. `copy` 到等长/四分之一长度的零尺寸目标；
5. `[][0]byte` 逐个 append 到一半长度；
6. 空 slice 自追加 2000 次；把已有元素追加到 cap=1024 的空 slice。

每轮结束显式 `runtime.GC()`，GC 后再次校验所有长度不变。`TORTURE_ROUNDS` 放大 append 总量。

# 期望行为

1. 所有 append 后 len 精确、cap 不小于 len；
2. 预分配容量被尊重，块追加不越界；
3. reslice/copy 的长度语义与普通元素一致；
4. 零尺寸 slice 自追加不产生错误长度；
5. GC 后长度与派生 slice 保持一致；最后一行输出 `ALL PASS l13_slice_zero_elems`。

# 失败判读

- `append len` / `cap below len`：零尺寸元素的 growslice 长度/容量计算错误；
- `make cap not honored`：make 容量语义错误；
- `reslice len` / `copy count`：零尺寸路径的边界计算错误；
- GC 后长度变化：GC 误改 slice header 或底层对象被提前回收。

# 参数

- `UAF_SEED`：每轮元素总量的种子，失败输出带该值；
- `TORTURE_ROUNDS`：轮数（默认 2，长压测可放大到数十轮）。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_slice_zero_elems/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
