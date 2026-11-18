---
id: l05_map_large_value_copy
dim: L05_alloc_gc
priority: P2
sources:
  - sonic@v1.13.2: issue_test/issue100_test.go:30 的 TestLargeMapValue：超过 128B 的 struct 作为 map value 由运行时按指针存储
  - badger@v4.5.0: value_test.go:337 的 TestValueGC3：100 个 32KB 大值写入与遍历
tags: [L05, map-churn, large-map-value, by-value-struct, pointer-fields, copy, gc, torture]
requires: []
runtime: "<10s（默认 2 轮）"
---

# 场景

`map[int64]mvVal` 的 value 是约 1.1KB 的按值 struct：内嵌 `[1024]byte`、string、
`[]byte` 与 `*mvMeta`。每轮插入 700 项并保存一份同类型独立副本；GC 后逐项校验
map 内值与副本；随后覆盖 1/3、删除 1/5，再 GC 校验新旧版本与删除语义；最后
整表丢弃、制造垃圾并 GC，只留副本。默认 2 轮，`TORTURE_ROUNDS` 可放大到 8 轮。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_map_large_value_copy`；
2. 建表并 GC 后，700 项的 id/ver/name/fixed/tail/meta 全部与种子推导值一致；
3. map 中按值取出的副本与独立副本的 `*mvMeta` 指针身份相同；
4. 覆盖 1/3 后，新值为 ver=2 且 meta 指针已更换；被覆盖前的旧副本仍为 ver=1；
5. 删除 1/5 后，被删 key 确实不存在，其余项版本/内容正确；
6. 整表丢弃 + GC 后，独立副本仍完整；最终每轮 pin 数量等于轮数。

## 失败判读

- `key=... missing` / `ver` / `fixed byte` / `tail`：map 大 value 存储或按值复制丢字段；
- `nil ptr` / `copy lost pointer identity`：value 内指针在 bucket 扫描或复制中丢失；
- `update reused old meta pointer`：覆盖路径错误复用了旧对象；
- `pin-after-drop`：map 释放后独立副本仍引用被回收内存（UAF）；
- clobberfree 下集中的 fixed/tail 失败：大 value 底层数组被提前复用。

## 参数

- `UAF_SEED`：决定 fixed/tail 的填充模式与 tail 长度；失败输出带种子；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–8。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_map_large_value_copy/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=6 /tmp/case
```
