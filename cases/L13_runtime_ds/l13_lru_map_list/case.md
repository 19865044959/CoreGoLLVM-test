---
id: l13_lru_map_list
dim: L13_runtime_ds
priority: P2
sources:
  - fiber@v2.52.6: middleware/cache/cache_test.go:712 Test_Cache_MaxBytesOrder MaxBytes=2 时按访问顺序淘汰，命中/未命中序列固定
  - gitea@v1.23.5: modules/regexplru/regexplru_test.go:12 TestRegexpLru 编译结果 LRU 复用与 Len 变化
tags: [L13, map, container/list, lru, eviction, move-to-front, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s（默认 3 轮；TORTURE_ROUNDS 可放大）"
---

# 场景

`map[string]*list.Element` + `container/list` 的 LRU 缓存：

1. 容量 3 的固定序列：插入 A/B/C、Get A 提升、插入 D 淘汰 B、更新 C 提升、Remove D，逐步校验前→后顺序与淘汰对象；
2. 每轮（默认 3 轮）容量 64、1800 次操作，操作脚本由 `UAF_SEED` 派生：45% Put、35% Get、20% Remove，key 空间 120（必然发生容量淘汰）；
3. 独立参考模型用有序字符串切片实现相同 LRU 语义，每次操作后比较顺序快照、Get 命中与 Put 淘汰结果；
4. 每 250 步 GC，校验 list 与 map 长度一致、双向索引一致、每个元素 payload 有效；
5. 每轮结束与 GC 后再次比较最终顺序，验证浅层指针跨 GC 保活。

# 期望行为

1. 命中把元素提升到队首；未命中不改变顺序；
2. 超容量插入淘汰队尾，被淘汰 key 立即查不到；
3. 更新已有 key 只替换 value 并提升，不产生新条目；
4. 随机操作序列的每一步都与参考模型完全一致；
5. list 与 map 的双向索引始终一致，GC 前后 payload 有效；
6. 最后一行 `ALL PASS l13_lru_map_list`。

# 失败判读

- `order mismatch`：move-to-front/淘汰次序错误；
- `eviction mismatch`：容量边界（>capacity 才淘汰）或淘汰对象选择错误；
- `list/map inconsistent`：删除 key 时未移除 list 元素，或反之；
- `payload corrupt` / GC 后崩溃：list 元素中的指针未被扫描（UAF）；
- `remove mismatch`：删除不存在 key 的行为与参考不一致。

# 参数

- `UAF_SEED`：操作脚本与丢弃键序列种子，失败输出带该值；
- `TORTURE_ROUNDS`：轮数，默认 3，范围 1..200。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_lru_map_list/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_ROUNDS=50 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
