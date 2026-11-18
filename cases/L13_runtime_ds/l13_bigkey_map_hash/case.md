---
id: l13_bigkey_map_hash
dim: L13_runtime_ds
priority: P2
sources:
  - sonic@v1.13.2: internal/caching/hashing_test.go:50 TestHashing_Fnv1a 对字符串逐字节计算 FNV-1a
  - badger@v4.5.0: db2_test.go:205 TestBigKeyValuePairs 65KB key 与超 ValueLog 的 value
  - go-redis@v9.8.0: internal/hashtag/hashtag_test.go:17 CRC16 对二进制 key 的分槽哈希
tags: [L13, map, large-key, fnv1a, hash-index, ownership, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s（默认 2 轮；TORTURE_ROUNDS 可放大）"
---

# 场景

模拟大 key 存储/索引中的哈希与等值路径：生成 120 个 1KB/4KB/8KB/16KB/32KB/64KB 的二进制 key（前 14 字节含 id，id%5==0 的 key 共享长公共前缀），执行：

1. `map[string]*bigEntry` 插入 120 项，同时建立 `map[uint64][]int` FNV-1a 哈希索引；每次 `string(buf)` 拷贝后立刻把源缓冲改写为 0xEE；
2. 用全新缓冲重建同内容 key，按 `UAF_SEED` 随机探测序逐项查询，并校验哈希桶归属；
3. 同 key 替换 value（条目数不变）；
4. 删除每 7 个 key，GC 后确认缺席，再用全新缓冲重插；
5. 64KB 公共前缀、仅尾字节为 'A'/'B' 的两个 key：哈希不同、查询不串、删除一个不影响另一个；
6. 最终按 id 顺序全量校验，GC 后再校验一次。

每 16 次插入、每阶段边界与最终校验前执行显式 `runtime.GC()` + 垃圾分配；`TORTURE_ROUNDS` 控制外层轮数，默认 2 轮，每轮使用不同 seed 派生 key。

# 期望行为

1. 插入后 map 与哈希索引覆盖全部 120 个 key，无重复；
2. 源缓冲改写不影响已有 key，重建 key 全部命中且 hash/size/sum 与内容一致；
3. 更新后条目数不变，更新项 value 为新指针且内容正确；
4. 删除项 GC 后不可见，重插后恢复为 120 项；
5. 长公共前缀 sibling key 的哈希与 map 查找完全独立；
6. 所有 GC 检查点后内容不变；
7. 最后一行 `ALL PASS l13_bigkey_map_hash`。

# 失败判读

- `bigkey: lookup miss/corrupt`：string key 未拷贝、map bucket 迁移丢 key 或哈希计算错误；
- `hash index missing`：哈希/相等性路径不一致；
- `long-prefix keys hash collision` 或 `lookup crossed`：长 key 比较/哈希截断错误；
- `SIGSEGV` / bad pointer：大对象跨 GC 存活问题；
- 超时：64KB key 全量重扫开销异常或死循环。

# 参数

- `UAF_SEED`：key 内容与探测顺序种子，失败信息附 seed；
- `TORTURE_ROUNDS`：外层轮数，默认 2，上限 20。

# 运行

```bash
go build -o /tmp/l13_bigkey_map_hash ./cases/L13_runtime_ds/l13_bigkey_map_hash/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/l13_bigkey_map_hash
UAF_SEED=2 TORTURE_ROUNDS=5 /tmp/l13_bigkey_map_hash
```
