---
id: l13_prefix_trie_match
dim: L13_runtime_ds
priority: P0
sources:
  - badger@v4.5.0: trie/trie_test.go:28 前缀 trie 多前缀匹配，返回去重 id 集合，含空前缀/重复插入
tags: [L13, trie, prefix-match, map, dedupe, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 badger 的前缀 trie 查询：键为字符串、值为 id 集合；`match(prefix)` 返回所有以 prefix 开头的键的 id 并集，去重后升序返回。空前缀命中整棵树。用例先做固定数据（重复 key/id、命中/未命中/挂前缀），再用 `UAF_SEED` 生成 400 次随机插入与 150 个随机前缀，与暴力 `strings.HasPrefix` 参考实现逐前缀对照。GC 插在建树后、随机查询前后，验证 trie 节点 map/children 指针在 GC 后仍有效。

# 期望行为

1. 固定数据下 `match("")` 返回 `[1 2 3 4 5]`，`match("ab")` 返回 `[1 2 3]`，`match("abc")` 返回 `[1 2]`，`match("abcd")` 返回 `[2]`，`match("zzz")` 为空；
2. 重复插入同一 `(key,id)` 不产生重复结果；
3. 修改一次返回的 id 切片不影响下一次查询结果（返回独立拷贝）；
4. 随机插入后，每个前缀的 trie 结果与暴力参考实现完全一致，且结果升序、无重复；
5. 追加一批新 id 并 GC 后，再次查询（含空前缀）仍与参考实现一致；
6. 最后一行 `ALL PASS l13_prefix_trie_match`。

# 失败判读

- trie 与参考结果不一致：前缀遍历/子树收集/去重排序逻辑错误；
- 结果含重复或乱序：id 集合未去重或未排序（map 遍历顺序泄漏）；
- 仅在 GC 后出错：trie 节点的 `children map[byte]*trieNode` 或 `ids []int` 未被正确扫描；
- 修改返回值影响后续查询：返回了内部切片而非拷贝。

# 参数

- `UAF_SEED`：随机键、随机 id 与前缀生成的种子（runner 每次重复注入不同值），失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_prefix_trie_match/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
