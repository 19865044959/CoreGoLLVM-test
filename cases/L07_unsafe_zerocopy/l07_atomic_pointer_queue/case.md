---
id: l07_atomic_pointer_queue
dim: L07_unsafe_zerocopy
priority: P2
sources:
  - nats-server@v2.11.0: server/sublist_test.go:957 不可变快照经 atomic.Pointer 发布，读侧无锁匹配
  - tailscale@v1.80.2: types/lazy/sync_test.go:127 万级并发首次 Get，初始化只执行一次（atomic 指针）
  - go-redis@v9.8.0: internal/pool/pool_test.go:307 Get/Put/Remove 并发操作连接槽位，槽位不得悬挂
tags: [L07, atomic, lock-free-queue, michael-scott, GC, concurrent]
requires: []
runtime: "<10s（TORTURE_ROUNDS 可放大）"
---

# 场景

`atomic.Pointer[T]` 实现 Michael-Scott 无锁队列：dummy head + tail 原子指针，enqueue 用
`next.CompareAndSwap(nil, node)` 发布节点并推进 tail，dequeue 用 head CAS 摘除并把已消费节点
转为新 dummy（清空 payload）。每个 payload 含 string、[]byte 与 `*leaf`，只经 atomic 队列
可达。覆盖顺序入队/出队、遍历、GC 后存活，以及 4 个生产者 + 单消费者的并发压测。

## 期望行为

1. 顺序入队 24 个后 GC，先出队 10 个 payload 的 seq 必须为 0..9；
2. 遍历剩余队列得到 seq 10..23，GC 后再次遍历仍全部正确且 payload 字段完整；
3. 再入队 16 个后遍历顺序为 10..39；全部出队后 `dequeue()==nil` 且遍历计数为 0；
4. 空队列 GC 后仍为空；
5. 并发阶段 4 个生产者各入队 `per*TORTURE_ROUNDS` 个唯一 seq，消费者收到的 seq 无重复、
   payload 的 tag/leaf/data 全部匹配，最终计数等于生产总数；
6. 并发期间穿插显式 GC 不产生悬挂 payload；队列只经 atomic.Pointer 字段可达；
7. 末行 `ALL PASS l07_atomic_pointer_queue`，失败退出 1。

## 失败判读

- 出队顺序错乱/丢节点：tail/next CAS 推进逻辑错误或 head CAS 竞态；
- 重复 seq：ABA/重复发布，或 dummy 转换时未正确摘链；
- GC 后读 payload 崩溃/efence 报错：atomic.Pointer 槽位未被扫描或节点被提前回收；
- 并发阶段死循环：队列空判定/tail 追赶逻辑错误，runner 超时；
- 仅 single/k8s（GOMAXPROCS 小）失败：自旋消费者饥饿或抢占问题。

## 参数

- `UAF_SEED`：决定并发每生产者基数（96..160）与 payload 字节模式；
- `TORTURE_ROUNDS`：并发批次数放大（默认 2，钳制到 1..16），默认运行 <10s。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_atomic_pointer_queue/*.go
UAF_SEED=1 TORTURE_ROUNDS=4 GODEBUG=clobberfree=1 /tmp/case
```
