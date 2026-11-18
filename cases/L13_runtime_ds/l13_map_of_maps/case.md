---
id: l13_map_of_maps
dim: L13_runtime_ds
priority: P1
sources:
  - goccy-json@v0.10.5: test/cover/cover_map_test.go:16 TestCoverMap 递归/嵌套 map 的编码覆盖
  - sonic@v1.13.2: decoder/decoder_test.go:47 TestGC 解码到 interface{} 后校验嵌套 map/slice/string 计数
tags: [L13, map-of-maps, nested, alias, nil-map, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

`map[string]map[string]*leaf` 两级索引与 `map[string]map[string]map[string]*leaf` 三级索引。外层 20 组（g00..g19）× 内层 12 键（k00..k11，按 (oi+ki)%4 跳过部分条目），叶子含 int、格式化字符串和 12B pad。依次执行：

1. 构建后 GC，按排序外层/内层 key 与模型逐项比对；
2. 内层替换（(oi+ki)%3==0）：新指针、新值；
3. 内层删除（ki%4==1）；
4. 外层删除（每 5 组删一组）；
5. 外层重插 g05/g10 两个新内层 map 并填 3 条；
6. 别名：`alias` 与 g01 共享同一内层 map，经 alias 写入对 g01 可见；删除 g01 外层键后 GC，alias 仍持有全部条目；
7. nil 内层 map：len/读取/delete 均安全，GC 后仍为 nil；
8. 三级嵌套 map 的构建、中间层删除、外层删除，与三级模型逐项比对。

每个阶段之间显式 GC + 垃圾分配；所有校验使用排序 key，不依赖 map 遍历顺序。

# 期望行为

1. 每次变更后外层/内层大小、key 集合、叶子 id/字符串/pad 与模型一致；
2. 内层更新后旧指针被替换为新值，GC 后不复活旧值；
3. 外层删除后对应内层 map 整体消失，其他组不受影响；
4. 重插的 g05/g10 只含 3 个指定键，值正确；
5. alias 写入在 g01 下可见；删除 g01 后 alias 仍能读到该值且 GC 后正确；
6. gNil 的 len 为 0、读取返回 nil、delete 不 panic；
7. 三级 map 删除 mx 后 ga 剩 2 个中间层，删除 gb 后只剩 ga，叶子跨 GC 正确；
8. 最后一行 `ALL PASS l13_map_of_maps`。

# 失败判读

- 排序 key 集合不一致：内层/外层删除或重插逻辑错误；
- 叶子值为旧值或 nil：map of maps 的指针未被写屏障正确记录；
- alias 写入不可见：外层 map 赋值被错误复制而非共享引用；
- GC 后叶子字段乱码：嵌套 map 中的指针未扫描（UAF）；
- 三级 map 计数错误：中间层/底层 map 的删除路径错误。

# 参数

- `UAF_SEED`：叶子 pad 字节与 salt 的种子，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_map_of_maps/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
