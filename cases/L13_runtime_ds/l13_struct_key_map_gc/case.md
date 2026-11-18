---
id: l13_struct_key_map_gc
dim: L13_runtime_ds
priority: P1
sources:
  - goccy/go-json@v0.10.5: decode_test.go:1047 `map[time.Time]string` 以结构体时间为 key 解码（:1052 另有 `map[Point]string` 路径）
  - sonic@v1.13.2: decode_test.go:747 `map[Point]string` 结构体 key 的类型错误路径
  - etcd@v3.5.0: server/etcdserver/api/rafthttp/urlpick_test.go:30 `map[url.URL]bool` 用结构体 URL 作 key 查询
tags: [L13, map, struct-key, nested-key, array-key, time-key, equality, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟配置/索引层用结构体值作 map key 的路径。`nodeKey` 由 int64、string、`[2]string` 与嵌套 `zone{x,y int64}` 组成；另测 `map[[3]int64]string` 与 `map[time.Time]string`（time.Time 内含 *Location 指针字段）：

1. 插入 1500 个 `map[nodeKey]*entry`；用 `string([]byte(s))` 重建等值但不同底层数组的 string 字段，按 `UAF_SEED` 打乱顺序逐项查找；
2. 每 4 个 key 覆盖 value（长度不变，旧指针必须被替换），GC 后全量校验；
3. 每 3 个 key 删除，GC 后确认缺席、邻近 key 不受影响；
4. 重插删除项并扩容到 3300 项，再次 GC 与全量校验；
5. 仅 `z.y`/`tags[1]`/`name` 不同的 sibling key 必须互不命中；
6. `[3]int64` 数组 key：512 项覆盖、删除、GC、重插；
7. `time.Time` key：256 个 Unix 秒时间，删除每 4 个、GC 后用同 instant 重建 key 查找、重插。

每个阶段后执行显式 `runtime.GC()` + 垃圾分配，校验不依赖 map 遍历顺序。

# 期望行为

1. 插入、覆盖、删除、重插各阶段长度与 key 集合正确，value 指针身份精确；
2. 重建的等价 key 全部命中，sibling key 全部不命中；
3. 数组 key 与时间 key 的删除/重插语义正确，GC 后内容不变；
4. map 遍历聚合值（`sum(id%1000)`）与模型一致；
5. 最后一行 `ALL PASS l13_struct_key_map_gc`。

# 失败判读

- `sibling key aliased`：结构体相等性/哈希只比较了部分字段；
- `lookup/value mismatch`：map 桶迁移后 key/value 丢失，或字符串 key 未正确参与比较；
- GC 后指针错乱：含 string/指针的 struct key 或 value 未被扫描（UAF）；
- `time key` 相关失败：time.Time 的 wall/ext/loc 比较路径异常。

# 参数

- `UAF_SEED`：key 名、value pad 内容与探测顺序的种子，失败输出带该值。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_struct_key_map_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
