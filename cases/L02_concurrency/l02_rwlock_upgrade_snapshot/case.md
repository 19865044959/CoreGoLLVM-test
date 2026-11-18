---
id: l02_rwlock_upgrade_snapshot
dim: L02_concurrency
priority: P1
sources:
  - etcd@v3.5.0: server/mvcc/kvstore_test.go:653 长读事务快照隔离，新读见新写、老快照仍见旧值
  - etcd@v3.5.0: server/mvcc/kvstore_test.go:715 100 读 + 100 写随机交错，读不得看到未提交写
  - caddy@v2.10.0: replacer_test.go:26（叠加 :240）RWMutex 保护的 map 上并发 ReplaceAll/Set/Delete
tags: [L02, RWMutex, lock-upgrade, double-check, immutable-value, snapshot, version, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟缓存 miss 后的“读锁升级写锁”模式，对应 etcd 快照隔离与 caddy RWMutex map 的并发读写。8 个读者对 4 个 key 同时 miss：每个读者先 `RLock` 查表，miss 后释放读锁，再获取串行化升级锁并二次检查，最后持写锁发布不可变 value。构建期间不持有读锁，也不持有全局写锁。

构建完成后进入并发阶段：8 个读者反复读取并校验版本与字节内容，2 个写者各对每个 key 更新 3 次，替换为新的不可变 value；乐观读者可能看到旧版或新版，但绝不能看到撕裂值。

# 期望行为

1. 第一轮构建中每个 key 只构建一次（`builds == 4`）；
2. 升级路径峰值并发为 1（升级锁串行化），且构建时未持有读锁；
3. 并发读只能看到完整版本（tag、version、data 校验一致），版本范围合法；
4. 全部写完成后每个 key 版本为 7，最终 GC 后内容不变；
5. 最后一行 `ALL PASS l02_rwlock_upgrade_snapshot`。

# 失败判读

- `builds != keys`：二次检查缺失或升级未串行化，重复构建；
- `upgrade peak`：升级锁未覆盖构建窗口；
- `torn value` / `out-of-range version`：读者读到了写者原地修改的中间态；
- `final version`：写者丢失更新或版本号未单调；
- 超时：读者在持读锁时等待升级锁导致死锁。

# 参数

- `UAF_SEED`：决定 value 字节内容与版本校验值；
- 无 TORTURE 参数（固定 4 key / 8 读者 / 2 写者）。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_rwlock_upgrade_snapshot/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
