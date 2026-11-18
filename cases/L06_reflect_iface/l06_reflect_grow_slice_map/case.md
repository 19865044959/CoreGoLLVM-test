---
id: l06_reflect_grow_slice_map
dim: L06_reflect_iface
priority: P2
sources:
  - gorm@v1.30.0: schema/relationship_test.go:483 自引用 many2many 的字段切片展开
  - echo@v4.13.4: context_test.go:656 参数切片按 maxParam 扩容/截断
  - pgx@v5.7.4: internal/pgio/write_test.go:23 预分配 cap 后 append 容量断言
tags: [L06, reflect, slice-growth, map-growth, Append, SetMapIndex, GC, torture]
requires: [reflect]
runtime: "<60s"
---

# 场景

用 `reflect.MakeSlice` 从 cap=1 开始逐个 `Append` `*node`，强制多次扩容并记录增长；用
`reflect.MakeMapWithSize` 从容量 1 开始逐个 `SetMapIndex` 触发 map 扩容，随后批量删除/再插入。
另做重叠 `reflect.Copy`、`AppendSlice` 与 `[]any` 扩容，每 128 个元素显式 GC，最后从全局接口槽回读。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_reflect_grow_slice_map`；
2. 扩容后 `len` 精确、`cap>=len`、至少发生一次 realloc，反射求和等于期望值；
3. map 插入/删除/再插入后的 Len 与元素和正确，指针字段可经 reflect 写入并被类型化视图看到；
4. 重叠 `reflect.Copy` 按 memmove 语义复制（dest 在 src 之后）；
5. 每次 GC 后新旧 backing array 中的 `*node` 均未被破坏，`[]any` 中的指针/map 元素保活；
6. 默认 8 轮 ×128 项在 60s 内完成，支持 `TORTURE_ROUNDS/TORTURE_ITEMS` 放大。

## 失败判读

- len/cap 或增长次数错误：reflect.Append 扩容逻辑错误；
- GC 后元素丢失/值错乱：slice/map 写屏障缺失或扩容时指针未正确复制；
- map Len/和错误：map 扩容/删除实现错误；
- 重叠 Copy 结果错误：memmove 语义未保持；
- 超时/内存爆炸：TORTURE 参数过大，或 map/slice 扩容路径退化。

## 参数

- `UAF_SEED`：随机种子，决定 map 插入顺序；失败输出带出该值；
- `TORTURE_ROUNDS`：轮数，默认 8，上限 2000；
- `TORTURE_ITEMS`：每轮元素数，默认 128，上限 20000（与轮数同时放大时注意内存）。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_grow_slice_map/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=200 TORTURE_ITEMS=2048 UAF_SEED=3 /tmp/case
```
