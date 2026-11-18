---
id: l01_generic_trie
dim: L01_generics
priority: P2
sources:
  - badger@v4.5.0: trie/trie_test.go:28 前缀 trie 多前缀匹配返回 id 集合，覆盖空前缀/重复 id
  - echo@v4.13.4: router_test.go:1368 插入 `/te/st*` 触发节点拆分，RouteNotFound handler 必须随节点迁移保留
  - gin@v1.10.0: tree_test.go:101 11 条含 Unicode 的静态路由插入 radix 树后逐条查找，含 key 不匹配/无子节点
tags: [L01, generics, trie, nested-map, prefix-query, ordered-walk, pointer-identity, gc, TORTURE_ROUNDS, UAF_SEED]
requires: [generics]
runtime: "<3s（默认约 1.1k key；TORTURE_ROUNDS≤16 约 0.5s）"
---

# 场景

实现 `Trie[V]`：泛型 `node[V]` 内嵌 `map[byte]*node[V]`、`*V` 载荷、has 标志与
子树计数。长压测插入约 1.1k–19k 个共享前缀的 key，覆盖写入、每 3 个覆盖一次、
每 5 个删除一次并做二次删除、删除后重插、空 key（根节点）、nil 值（has 区分）、
前缀计数与按字节序 DFS 遍历。对应 badger 前缀 trie、echo 路由节点拆分与 gin radix 树。

# 期望行为

1. 覆盖写入替换指针但 size 不变；删除后 key 不可见，二次删除返回 false；重插恢复可见；
2. 空 key 挂在根节点，`PrefixCount("")` 等于 size；nil 值以 has 标志保留为“存在”；
3. 所有存活 key 的指针身份与 ID/Name/Score 在 GC 后正确，删除 key 不复活；
4. 13 个前缀组的 `PrefixCount` 与逐 key `strings.HasPrefix` 复算一致，缺失前缀为 0；
5. `Walk` 按字节序严格递增输出，访问数等于 size，载荷 ID 和与复算一致；
6. 空 trie 的 Get/Delete/PrefixCount/Walk/Len 边界正确，单条插入可回读；
7. `Trie[int64]` 第二实例化在 GC 后长度、前缀计数、载荷和正确；
8. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_generic_trie`。

# 失败判读

- `... pointer identity after GC` / `payload corrupted`：嵌套 map 中的 `*V` 未被扫描或节点布局错误；
- `delete of existing trie key failed` / `deleted trie key became visible`：删除/剪枝或 has 计数错误；
- `prefix ... count mismatch`：子树计数在覆盖/删除/重插路径上未维护；
- `walk order is not strictly increasing`：子节点遍历顺序或 key 拼接错误；
- `trie len ...`：size 与 has 状态不一致；
- `FAILED ... seed=`：失败输出带 UAF_SEED。

# 参数

- `UAF_SEED`：决定 key 数（约 1100..1300），失败输出带种子；
- `TORTURE_ROUNDS`：key 数倍数（默认 1；上限 16，约 1.9 万 key）。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_generic_trie/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
