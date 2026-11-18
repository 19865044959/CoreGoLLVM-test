---
id: l13_string_compare_order
dim: L13_runtime_ds
priority: P0
sources:
  - sonic@v1.13.2: issue_test/issue115_test.go:76 TestIssue115_MarshalLargeIntKeyMapWitSort 1 万个 int key 排序后编码
  - etcd@v3.5.0: pkg/adt/interval_tree_test.go:406 TestIntervalTreeSortedVisit 中序访问严格有序
  - badger@v4.5.0: table/merge_iterator_test.go:162 TestMergeMore 多路有序 key 归并与重复取舍
tags: [L13, string, compare, sort, binary-search, map-key, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

字符串比较/排序的字节序语义：

1. 1..16384 字节公共前缀下仅尾字节不同、仅首字节不同、前缀关系、同内容不同分配的 `strings.Compare`/`<`/`>`/`==` 全部与手写逐字节参考一致；
2. 256 个单字节串 0x00..0xFF：`sort.Strings` 后必须严格递增，二分查找每个字节都命中，GC 后端点不变；
3. `UAF_SEED` 生成 3000 对随机字节串（含 0x00/0x7F/0x80/0xFF 与相同内容），比较符号与参考一致，每 257 对 GC 后复查；
4. 240 个 2048B 公共前缀 + 5 位 id 的 key：乱序插入 `map[string]*scRec`，排序、二分、逐个 map 查询，GC 后用全新字符串复查；
5. 校验不存在 key 的插入点（中间、前缀、全部之后）与空串/NUL 边界。

# 期望行为

1. 所有比较结果与逐字节（无符号）参考一致；
2. 单字节全集严格递增且二分命中；
3. 随机 pair 的符号在 GC 前后一致；
4. 240 个长 key 排序无逆序，map 查询全部命中且 pad 正确，GC 后不变；
5. 插入点位置精确；
6. 最后一行 `ALL PASS l13_string_compare_order`。

# 失败判读

- `strings.Compare got=` / `operator mismatch`：比较被优化成错误的符号/长度语义；
- `order broken` / `binary search miss`：sort 或二分边界错误；
- `lookup bad` / `after GC bad`：长字符串 map key 等值或所有权错误；
- `insertion point`：二分插入点偏移；
- 超时：长公共前缀比较退化为异常复杂度。

# 参数

- `UAF_SEED`：随机字节对与插入顺序种子，失败信息附 seed。

# 运行

```bash
go build -o /tmp/l13_string_compare_order ./cases/L13_runtime_ds/l13_string_compare_order/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/l13_string_compare_order
```
