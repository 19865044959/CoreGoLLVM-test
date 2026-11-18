---
id: l07_generic_unsafe_ring
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - kitex@v0.13.0: pkg/remote/trans/ttstream/container/queue_test.go:26 泛型 Queue[int] 生产者/消费者实例化
  - quic-go@v0.50.1: internal/utils/ringbuffer/ringbuffer_test.go:9 泛型 RingBuffer 的 Push/Peek/Pop 与回绕
  - nats-server@v2.11.0: server/ring_test.go:22 环形缓冲覆写旧元素，len/total 与遍历顺序校验
tags: [L07, unsafe, generics, ring-buffer, Sizeof, GC]
requires: []
runtime: "<10s"
---

# 场景

泛型 unsafe 容器：`gring[T]` 用 `unsafe.Pointer` 指向 `make([]T, capa)` 堆数组，以
`unsafe.Sizeof(zero T)` 为步长、`unsafe.Add` 做物理槽位寻址，逻辑访问经 head/len 取模回绕。
分别实例化为 int64、含 string/`*gLeaf`/[]byte 的 gCell、`*gLeaf` 与自定义 `myByte`。
验证满/空、回绕顺序、pop 清零、GC 扫描以及丢弃环后仅保留内部槽位指针的存活。

## 期望行为

1. 各实例化 `stride == Sizeof(T)`，槽位地址差等于 stride 且按 T 对齐；
2. int64 环满时拒绝 push，弹出 0..2 后回绕 push 100..102，逻辑顺序为 3..7,100..102；
3. gCell 环回绕后逻辑顺序正确，所有 string/leaf/data 字段完整；
4. pop 后槽位类型化清零，物理死槽位的 id/name/link/data 均为零值；
5. GC 后环内 gCell 与已弹出 gCell 的指针字段全部有效；
6. 丢弃 `*gring[gCell]`、仅保留首槽内部指针并 GC 后，所有物理槽位（存活或已清零）状态正确；
7. `*gLeaf` 元素环的指针元素跨 GC 存活，弹出后清空；`myByte` 实例化 stride==1；
8. 末行 `ALL PASS l07_generic_unsafe_ring`，失败退出 1。

## 失败判读

- stride/地址差错误：`Sizeof(T)` 或 `unsafe.Add` 步长计算错误；
- 回绕顺序错误：head/len 取模逻辑错误；
- pop 后槽位仍持有对象：清零路径未走类型化赋值/写屏障；
- GC 后元素指针悬空/崩溃：unsafe.Pointer 数组未被扫描，或 `make([]T)` 底数组被提前回收；
- 仅 efence/clobberfree 失败：死槽位清零或内部指针保活问题。

## 参数

- `UAF_SEED`：决定 gCell 环容量（6..7）与全部值模式，失败信息带种子；
- 无 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_generic_unsafe_ring/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
