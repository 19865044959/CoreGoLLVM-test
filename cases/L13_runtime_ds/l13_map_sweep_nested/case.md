---
id: l13_map_sweep_nested
dim: L13_runtime_ds
priority: P1
sources:
  - etcd@v3.5.0: server/lease/lessor_test.go:105 TestLeaseConcurrentKeys itemSet 并发写删与全量 Keys 遍历
  - etcd@v3.5.0: client/pkg/types/set_test.go:27 TestThreadsafeSet Add/Contains/Values/Remove 全套集合操作
  - badger@v4.5.0: db_test.go:249 20 goroutine 写 500 条后单迭代器全量顺序校验
tags: [L13, map, delete-during-range, nested, alias, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

`map[string]map[string]*sweepVal` 的嵌套清除（64 组，内层 0..31 项，由 `UAF_SEED` 决定）：

1. 外层 range 中逐个进入内层 range，按 `(g*131+k*17+seed)%5` 删除当前内层键；随后按 `(g*31+seed)%7` 删除当前外层组。统计内层/外层访问次数必须等于原始条目数/组数；
2. 删除后按排序 id 逐一检查保留/缺席，GC 后再检查所有保留 value 的 id/num/pad；
3. 第二阶段：内层删光当前键、外层删光当前组，最终 map 为空，访问计数与剩余条目精确匹配；
4. 别名快照：取 g05 的内层 map，删除外层键并 GC，快照仍保有全部 value；通过快照删除偶数键后自身一致；
5. nil 内层 map 的 len/读取/delete 安全，GC 后仍为 nil。

每个阶段多次显式 GC + 垃圾分配；所有存在性校验基于 key 谓词，不依赖 map 遍历顺序。

# 期望行为

1. 每层 range 中删除当前键不会跳过其他原始键，访问计数精确；
2. 最终保留集合与谓词模型完全一致，GC 后不变；
3. delete-all 后 map 大小为 0 且访问计数匹配；
4. 外层删除后内层快照仍有效，快照自身删改正确；
5. nil 内层 map 操作不 panic；
6. 最后一行 `ALL PASS l13_map_sweep_nested`。

# 失败判读

- `outer iterations != original groups` / `inner iterations != original entries`：遍历中删除语义被破坏；
- `group presence mismatch` / `inner count`：删除/保留集合错误；
- `snapshot ... after GC`：外层删除过早释放内层 map；
- nil 相关 FAIL：nil map 读取/delete 语义错误；
- 超时：range 与 delete 组合异常。

# 参数

- `UAF_SEED`：内层数量与删除谓词种子，失败信息附 seed。

# 运行

```bash
go build -o /tmp/l13_map_sweep_nested ./cases/L13_runtime_ds/l13_map_sweep_nested/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/l13_map_sweep_nested
```
