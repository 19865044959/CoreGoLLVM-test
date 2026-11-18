---
id: l18_map_ptr_key_churn
dim: L18_gc_lifecycle_wb
priority: P0
sources:
  - badger@v4.5.0: db_test.go:249（20 goroutine 共写 1 万 key，扩容后全量顺序校验）
  - go-redis@v9.8.0: internal_test.go:86（Ring SetAddrs 与 GetByKey 并发，分片槽位持续替换）
tags: [L18, write-barrier, map, pointer-key, delete-reinsert, noinline, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 8 轮；TORTURE_ROUNDS 上限 32）"
---

# 场景

全局老 `map[*pkNode]*pkNode` 含 64 个指针键值对。每轮按 `UAF_SEED` 打乱槽位：
noinline `pkDelete` 删除 16 个键，被删旧键转入全局 retired slice 继续可达；
noinline `pkInsert` 用全新年轻键/值替代这 16 个槽位；noinline `pkOverwrite`
对另 16 个槽位保持旧键、换入年轻值。GC 后按固定槽位顺序校验 map 条目指针
身份、键/值 payload，以及 retired 旧键既不在 map 中、内容又未被回收。

与 `map_old_key_new_value_grow`（整数键、只增不减）不同，本用例覆盖指针键的
删除-重插、键指针身份替换与 retired 键根扫描。

## 期望行为

1. 每轮结束后 map 长度恒为 64，无已删键残留；
2. 每个槽位的 `m[keys[i]] == vals[i]`，键与值 payload 逐字节正确；
3. 被替换下的旧键在 retired slice 中跨 GC 存活，且不再命中 map；
4. 新插入的年轻键值跨 GC 存活，旧键新旧代次混合无误；
5. 失败信息带 `seed=<UAF_SEED>`，同种子可复现删除顺序；
6. 退出码 0，最后一行 `ALL PASS l18_map_ptr_key_churn`。

## 失败判读

- `map len=...`：删除或插入计数错误，先排除提前回收；
- `value pointer mismatch`：map value 写屏障或槽位复用错误；
- `key ... corrupted`：指针键对象被误回收（map key 未被扫描）；
- `retired key ... still in map`：delete 未真正移除哈希项；
- `retired-key ... corrupted`：retired slice 根或旧键对象生命周期错误。

## 参数

- `UAF_SEED`：槽位排列、值数据模式与失败信息；
- `TORTURE_ROUNDS`：轮数（默认 8，上限 32）。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_map_ptr_key_churn/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=24 UAF_SEED=7 /tmp/case
```
