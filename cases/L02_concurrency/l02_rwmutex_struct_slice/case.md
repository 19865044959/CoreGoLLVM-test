---
id: l02_rwmutex_struct_slice
dim: L02_concurrency
priority: P1
sources:
  - etcd@v3.5.0: server/mvcc/kvstore_test.go:715 100 读 + 100 写随机交错，读不得看到未提交写
  - etcd@v3.5.0: server/mvcc/kvstore_test.go:653 长读事务快照隔离，新读见新写、老快照仍见旧值
  - caddy@v2.10.0: replacer_test.go:26 RWMutex 保护 map 上并发 ReplaceAll/Set/Delete
tags: [L02, RWMutex, struct-slice, snapshot-copy, in-place-update, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 etcd kvstore 的读快照隔离与 caddy 的 RWMutex 共享结构：`[]record` 直接存放 struct 值（128 行，含 string、seed 派生 tag 与 probe 数组），`sync.RWMutex` 保护整个切片。

4 个写者各自独占 32 行区间，每轮在写锁内原地整体替换本区间的 record（gen 递增到 60）；4 个读者每轮在读锁内 `copy` 一个 16 行窗口到本地切片，解锁后在锁外逐条校验，并在 GC 后再次校验同一份拷贝，证明快照拷贝与写者隔离且跨 GC 存活。写者每 17 轮、读者每 23 次显式 GC。

# 期望行为

1. 读者拷贝的每条 record 满足 `val == id*1000+gen`、tag/probe 与 gen 的固定关系，gen 在 `[0,60]`；
2. 拷贝在解锁并 GC 后内容不变（值拷贝独立于写者后续原地更新）；
3. 最终每个写者区间的 128 行全部为 `gen == 60`，切片长度仍为 128；
4. 最终校验和等于 `Σ(id*1000+60)`；写后窗口快照全部看到第 60 代；
5. 最后一行 `ALL PASS l02_rwmutex_struct_slice`。

# 失败判读

- `reader copied invalid record`：读写锁失效导致读到半更新 struct，或拷贝逻辑错误；
- `copied record snapshot corrupted after GC`：struct 内 string/数组数据跨 GC 被破坏；
- `final records` / `slice length changed`：最终状态丢失或被并发扩容破坏；
- `final checksum`：原地更新丢写或代数错误。

# 参数

- `UAF_SEED`：决定 record tag 派生值，失败输出带种子；
- 无 TORTURE 参数（P1 固定 128 行 × 60 轮）。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_rwmutex_struct_slice/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
