---
id: l02_rwmutex_shared_map
dim: L02_concurrency
priority: P1
sources:
  - etcd@v3.5.0: server/lease/lessor_test.go:105 Detach 与 10 个 Keys 并发，RWMutex map 读写竞态
  - caddy@v2.10.0: replacer_test.go:26（叠加 :240）RWMutex map 上并发 ReplaceAll/Set/Delete
  - tailscale@v1.80.2: syncs/shardedmap_test.go:8 分片 map 的 Set/Delete/Mutate 三态
tags: [L02, RWMutex, shared-map, pointer-record, snapshot, gc]
requires: []
runtime: "<2s"
---

# 场景

模拟 etcd lease store 与 caddy replacer 的读多写少共享表：64 个键由 4 个写者分段持有，写者每轮构造一个新的不可变 `*record`，在 `Lock` 下同时替换 `map[int]*record` 与 `[]*record`；4 个读者在 `RLock` 下快照 map 与 slice 中的记录指针，解锁后每 20 次读取分配垃圾并 `runtime.GC()`，GC 后重新校验快照内容。写者不修改旧记录，读者可以安全地在锁外继续访问快照。

# 期望行为

1. 读到的每个快照都满足 `key/gen/val/probe` 内部一致，`gen` 在 0..89 范围内；
2. map 与 slice 快照指向同内容的记录，快照跨 GC 后字段不被破坏；
3. 全部写者完成后，每个键的 map 与 slice 指向同一条记录，`gen` 为该键最后一次写入的轮次，`val == key*1000+gen`；
4. map 长度恒为 64，最后一行 `ALL PASS l02_rwmutex_shared_map`。

# 失败判读

- `map/slice record invalid` / `snapshot corrupted after GC`：锁区间内复制出的指针内容被 GC 破坏，疑似写屏障或栈位图问题；
- `final store invalid entries`：并发写导致 map/slice 更新不一致或最后一轮写入丢失；
- 超时：RWMutex 死锁或读者被写者长期饿死。

# 参数

无随机路径；`UAF_SEED` 由 runner 注入但本用例不使用。无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_rwmutex_shared_map/*.go
GODEBUG=clobberfree=1 /tmp/case
```
