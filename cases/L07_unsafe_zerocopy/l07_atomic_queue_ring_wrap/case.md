---
id: l07_atomic_queue_ring_wrap
dim: L07_unsafe_zerocopy
priority: P2
sources:
  - nats-server@v2.11.0: server/ring_test.go:22 容量 10 的环形缓冲 append 30 次，len 固定 10、顺序可校验
  - go-redis@v9.8.0: internal/pool/pool_test.go:307 Get/Put/Remove 并发操作连接槽位，槽位不得悬挂
  - nats-server@v2.11.0: server/sublist_test.go:957 不可变快照经 atomic.Pointer 发布，读侧无锁
tags: [L07, atomic, ring-queue, spsc, slot-reuse, wrap-around, GC]
requires: []
runtime: "<10s（TORTURE_ROUNDS 可放大）"
---

# 场景

atomic 指针环形队列的回绕边界：容量为 7..11（非 2 的幂），槽位数组元素为
`atomic.Pointer[aqPayload]`。单生产者用 `CompareAndSwap(nil,p)` 占用槽位后推进 tail，
单消费者读取并立即 `Store(nil)` 清槽后推进 head。payload 含 string/[]byte/*leaf。
覆盖填满拒绝、消费即清槽、回绕复用多圈、并发 SPSC 与显式 GC。

## 期望行为

1. 填满后 `tryPush` 返回 false，不覆盖任何未消费槽位；
2. 按 FIFO 排空时 seq 严格递增，payload 字段完整；
3. 每次消费后该物理槽位为 nil；排空并 GC 后所有槽位全为 nil；
4. 回绕交错 push/pop 多圈后逻辑顺序仍正确，槽位复用不产生重复/丢失；
5. 并发阶段生产者推入 `total` 条，消费者收到的 seq 无跳号、无重复，字段全部匹配；
6. 并发途中显式 GC 不产生悬挂 payload，结束后 head/tail 计数一致且队列为空；
7. `TORTURE_ROUNDS` 放大并发量（默认 2，上限 16）；
8. 末行 `ALL PASS l07_atomic_queue_ring_wrap`，失败退出 1。

## 失败判读

- 顺序错乱/丢条：head/tail 计数或取模索引计算错误，或清槽早于消费者读完；
- 重复 seq：回绕时旧槽位未清空，生产者 CAS 失败后覆盖了未消费 payload；
- GC 后崩溃：atomic.Pointer 槽位未被扫描，或 payload 只经裸指针可达；
- 并发死循环：满判定与消费者清槽窗口重叠导致永久自旋，runner 超时；
- 仅 single/k8s 失败：GOMAXPROCS=1 下 Gosched 退让不足。

## 参数

- `UAF_SEED`：决定容量 7..11、payload 字节模式与并发总量余数，失败信息带种子；
- `TORTURE_ROUNDS`：并发总量放大（默认 2，钳制到 1..16），默认运行 <10s。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_atomic_queue_ring_wrap/*.go
UAF_SEED=1 TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
