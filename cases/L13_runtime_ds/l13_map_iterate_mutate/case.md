---
id: l13_map_iterate_mutate
dim: L13_runtime_ds
priority: P0
sources:
  - etcd@v3.5.0: server/mvcc/kvstore_test.go:424 TestRestoreDelete 单 goroutine 在 keys map 上随机 range 后 delete（:446）
  - goccy-json@v0.10.5: decode_test.go:2457 TestPrefilled 预填充 map 后覆盖/复用条目
tags: [L13, map, iteration, delete-during-range, insert-during-range, gc]
requires: []
runtime: "<3s"
---

# 场景

单 goroutine 下三种典型的“边遍历边修改”写法：

1. range 中删除当前键（k%3==0）并插入新键 k+1500（k%5==0），只删除已到达的键，因此每个原始键必须恰好访问一次，新键可访问也可跳过；
2. range 中删除每个当前键，最终 map 必须为空且访问次数等于起始长度；
3. range 中只插入 25 个新键，所有原始键必须访问到，新键在循环结束后全部存在且值正确。

GC 与垃圾分配穿插在遍历过程中（每 256/300/200 个原始键），结束后再显式 GC 并按已知键逐个校验指针值、name 与 data，不依赖 map 遍历顺序。

# 期望行为

1. 阶段 1 访问原始键数 == 1500，删除数 == 500，插入数 == 300；
2. 阶段 1 结束后 len == 1300，已删除的 500 个键不存在，保留键和 300 个新键的值全部正确；
3. 阶段 2 对 1300 个键逐一删除，访问数 == 1300，GC 后 len == 0；
4. 阶段 3 原始 800 个键全部访问且值正确，成功插入 25 个键，len == 825，插入键的值跨 GC 有效；
5. 所有阶段的值校验包括 `*item` 的 id、name 和 data 字节；
6. 最后一行 `ALL PASS l13_map_iterate_mutate`。

# 失败判读

- “not every original key was visited”：迭代器漏桶/重复访问，或删除逻辑误删了未到达的键；
- map size 与删除/插入计数不符：插入的键被后续循环再次处理（本用例应只处理原始键）；
- GC 后 item 字段乱码或 nil：map value 指针未被正确扫描；
- 阶段 2 len 非 0：删除当前键的语义实现错误。

# 参数

无随机路径，不使用 `UAF_SEED`。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_map_iterate_mutate/*.go
GODEBUG=clobberfree=1 /tmp/case
```
