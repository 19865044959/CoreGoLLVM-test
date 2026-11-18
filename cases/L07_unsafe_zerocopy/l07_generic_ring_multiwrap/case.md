---
id: l07_generic_ring_multiwrap
dim: L07_unsafe_zerocopy
priority: P2
sources:
  - quic-go@v0.50.1: internal/utils/ringbuffer/ringbuffer_test.go:9 泛型 RingBuffer 的 Push/Peek/Pop 与回绕
  - nats-server@v2.11.0: server/ring_test.go:22 环形缓冲覆写旧元素，len/total 与遍历顺序校验
  - kitex@v0.13.0: pkg/remote/trans/ttstream/container/queue_test.go:26 泛型 Queue[int] 生产者/消费者实例化
tags: [L07, unsafe, generics, ring-buffer, multi-wrap, slot-zeroing, GC]
requires: []
runtime: "<10s（TORTURE_ROUNDS 可放大）"
---

# 场景

泛型 unsafe 环的多圈回绕：`grRing[T]` 用 `unsafe.Pointer + Sizeof(T)` 在堆数组上做槽位
寻址，容量 7..10（非 2 的幂）。分别实例化为含 string/[]byte/*leaf 的 `grCell`、`*grLeaf`
与双指针 `grPair`。多圈 push/pop 反复覆写槽位，每若干步显式 GC 并按物理槽位审计：逻辑
存活槽必须与期望 id 序列自洽，已腾空槽必须全部字段零化（不残留旧指针）。模拟 quic-go
泛型 RingBuffer、nats ring 覆写与 kitex 泛型队列的槽位复用。

## 期望行为

1. 填满后 push 返回 false；GC 后按 FIFO 顺序与物理槽位审计通过；
2. 多圈回绕中每次 pop 立即零化被腾空的槽位，逻辑顺序不丢不重；
3. 每 `capa` 步 GC 并审计：live 槽对应期望 id，dead 槽 id/name/link/data 全为零值；
4. 排空后空环 pop 返回 false，GC 后所有槽位仍为零；
5. `*grLeaf` 环与 `grPair` 环弹出后槽位为 nil，GC 后死槽保持 nil；
6. 丢弃环变量、仅保留已弹出 cell 后，跨显式 GC 内容完整；
7. `TORTURE_ROUNDS` 放大回绕圈数（默认 2，上限 12）；
8. 末行 `ALL PASS l07_generic_ring_multiwrap`，失败退出 1。

## 失败判读

- FIFO 顺序错乱：环形索引/取模或 head 推进错误；
- 死槽非零：pop 未清槽，旧指针仍被 GC 扫描（内存泄漏/悬挂引用）；
- GC 后 live 槽内容错乱：unsafe.Pointer 槽位未被扫描或步长计算错误；
- 重复 id：回绕覆盖了未消费槽位；
- 仅 stress/clobberfree 失败：清槽后仍读取旧值，或零化未生效。

## 参数

- `UAF_SEED`：决定容量 7..10、payload 字节模式与回绕工作量，失败信息带种子；
- `TORTURE_ROUNDS`：回绕圈数（默认 2，钳制到 1..12），每圈 `6*cap` 次 push。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_generic_ring_multiwrap/*.go
UAF_SEED=1 TORTURE_ROUNDS=6 GODEBUG=clobberfree=1 /tmp/case
```
