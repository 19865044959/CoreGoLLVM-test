---
id: l03_atomic_pointer_snapshot
dim: L03_async_preempt
priority: P1
sources:
  - etcd@v3.5.0: server/mvcc/kvstore_test.go:715 100 个写 goroutine 与 100 个读 goroutine 随机交错，读侧原子取快照，不得看到未提交写
  - badger@v4.5.0: skl/skl_test.go:197 同一 key 100 写 100 读，至少一次读到合法值，覆盖 atomic.Value 快照读写
tags: [L03, async-preempt, atomic.Pointer, CAS, snapshot, pointer-liveness, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

主 goroutine 每轮构造不可变配置快照，用 `atomic.Pointer.CompareAndSwap` 发布，随后 Load 取回并跨显式 GC 解引用节点；后台 reader 持续 Load 并校验 `ver/node/sum` 三者自洽。GC pump 并发执行，主循环每 1024 轮显式 GC。

# 期望行为

1. 每次 CAS 都成功发布当轮快照，Load 取回的版本号等于当轮；
2. 快照节点在显式 GC 后仍可读，值等于 `ver*7+1`；
3. 主 goroutine 的快照校验和与闭式参考值一致；
4. 后台 reader 完成 20000 次读取，未观察到不自洽快照；
5. 主循环显式 GC 次数为 19；
6. 最终发布快照的版本与节点内容正确；
7. GC pump 至少完成一轮，退出码 0，最后一行 `ALL PASS l03_atomic_pointer_snapshot`。

# 失败判读

- `FAIL: CompareAndSwap published each snapshot`：CAS 循环被错误优化或指针相等性判断异常；
- `FAIL: snapshot node readable after GC`：快照中的节点未被正确标记；
- `FAIL: concurrent reader saw only consistent snapshots`：读者看到半更新配置，发布顺序/可见性错误；
- `FAIL: final published snapshot intact`：最终快照被覆盖或回收。

# 参数

- `UAF_SEED`：决定后台 reader 的 Gosched 周期，失败信息带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_atomic_pointer_snapshot/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
