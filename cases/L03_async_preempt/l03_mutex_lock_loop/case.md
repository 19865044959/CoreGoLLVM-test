---
id: l03_mutex_lock_loop
dim: L03_async_preempt
priority: P1
sources:
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:37 100 个 goroutine 在锁内记录调度顺序并 TrySchedule，单消费者串行执行
  - etcd@v3.5.0: server/lease/lessor_test.go:105 一个 goroutine Detach 10 个 item，同时 10 个 goroutine 读 lease.Keys()，验证并发安全
tags: [L03, async-preempt, mutex, lock-unlock-loop, contention, pointer-holder, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

主 goroutine 与 helper goroutine 各做 131072 轮 `sync.Mutex` 加锁/解锁：双方都在锁内自增受保护计数，helper 同时在锁内轮换共享的节点指针（两个节点值相同、身份不同），主 goroutine 在锁内取节点指针、解锁后先显式 GC（每 4096 轮）再在无调用紧循环中解引用该指针。helper 每 16384 轮显式 GC，另有 GC pump 并发执行 8 轮 GC。初始节点由 `UAF_SEED` 决定。

# 期望行为

1. 进程退出码为 0；
2. 最后一行输出 `ALL PASS l03_mutex_lock_loop`；
3. 受 mutex 保护的计数等于 2×131072，没有丢更新；
4. 主 goroutine 解锁后取得的指针在显式 GC 后仍可读，解引用结果全为 100 且 tag 正确；
5. 主循环校验和与 helper 显式 GC 次数均符合预期；
6. 并发 GC pump 至少完成一轮；
7. 共享节点与两个底层节点在 GC 后保持原值，共享指针非空；
8. 在 base/clobberfree/efence/k8s 等 profile 下输出与校验结果一致。

## 失败判读

- `FAIL: mutex-protected counter lost no updates`：锁语义或计数更新丢失（也可能是用例逻辑错误）；
- `FAIL: node pointer readable after unlock+GC`：解锁后指针未跨 GC 保活或指向被回收对象；
- `FAIL: shared node intact after final GC`：共享结构被破坏；
- `fatal error: sync: unlock of unlocked mutex` 等：加解锁配对错误；
- 超时：锁竞争死锁。

## 参数

- `UAF_SEED`：决定共享节点初值与 helper 轮换起点；失败信息带出种子；
- 无其他放大参数，P1 固定规模。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_mutex_lock_loop/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
