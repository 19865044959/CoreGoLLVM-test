---
id: l13_container_ring_rewire
dim: L13_runtime_ds
priority: P2
sources:
  - quic-go@v0.50.1: internal/utils/ringbuffer/ringbuffer_test.go:11 定长 ring 的初始 Len 断言
  - quic-go@v0.50.1: framer_test.go:338 帧数据在 ringbuffer 中回绕保留
  - badger@v4.5.0: y/y_test.go:143 `rb2 := b.Bytes()[:512]` ring buffer 回绕后分段读取
tags: [L13, container/ring, link, unlink, move, pointer-value, rewire, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s（默认 3 轮；TORTURE_ROUNDS 可放大）"
---

# 场景

模拟环形队列/窗口在回绕、摘除与重连时的指针元素管理（quic-go ringbuffer、badger 回绕读取）：

1. 建立 240 个元素的 `ring.Ring`，Value 为 `*rgItem`，用 `Do` 校验初始顺序；
2. 在元素 40 后 `Unlink(17)`：主环长度变为 223，被摘子环内容为 41..57；
3. 把子环 `Link` 回元素 10 之后，校验完整顺序；
4. `Move(13)` 改变头指针，`Do` 序列应为旋转后的顺序；
5. `TORTURE_ROUNDS` 轮：每次从 head 后 `Unlink(m)`、在随机位置重新 `Link`，校验元素 id 集合完整、无重复、id 求和正确。

每轮后显式 `runtime.GC()`，校验所有 ring Value 指向对象的 name/pad 未损坏。

# 期望行为

1. Unlink/Link 后主环与子环的长度、顺序与模型一致；
2. Move 后 `Do` 从新头开始且 Prev/Next 自洽；
3. 反复重连后 240 个 id 恰好各出现一次；
4. GC 后 ring 中的指针 Value 仍指向有效对象；
5. 最后一行输出 `ALL PASS l13_container_ring_rewire`。

# 失败判读

- `len after unlink/link`：环长度维护错误；
- `order after link/move`：Link 插入位置或 Move 步进错误；
- `duplicate or missing element`：重连丢失节点或形成错误环；
- GC 后 payload 损坏：ring 节点中的指针 Value 漏扫（UAF）。

# 参数

- `UAF_SEED`：重连轮次中摘除长度与插入位置的种子，失败输出带该值；
- `TORTURE_ROUNDS`：重连轮数（默认 3）。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_container_ring_rewire/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
