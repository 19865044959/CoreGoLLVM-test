---
id: l01_generic_slice_ops
dim: L01_generics
priority: P1
sources:
  - pgx@v5.7.4: rows_test.go:335 CollectRows 用泛型收集器按序保存结果
  - sqlc@v1.28.0: internal/endtoend/testdata/emit_enum_valid_and_values/go/models.go:65 生成物 AllValues[T] 枚举切片
  - ent@v0.14.4: entql/types_test.go:14 谓词过滤 And/Or/Not 组合
  - go-redis@v9.8.0: internal/proto/scan_test.go:25 ScanSlice 将 []string 解到 []T 与 []*T
tags: [L01, generics, slice-utils, map-filter-reduce, chunk, distinct, group-by, flat-map, zip, ordering, gc, UAF_SEED]
requires: [generics]
runtime: "<10s"
---

# 场景

泛型切片工具集：`mapSlice[T,U]`、`filterSlice[T]`、`reduceSlice[T,A]`、`chunkSlice[T]`、`distinctSlice[T,K]`、`groupBySlice[T,K]`、`flatMapSlice[T,U]`、`zipSlice[T,U]`，全部只依赖标准库并保持输入顺序。对应结果收集器、生成代码的枚举切片、谓词过滤与反射扫描后的元素转换。

以 UAF_SEED 洗牌 260–320 个 `*rec` 作为输入，对每个算子用独立循环复算期望顺序/指针/计数并逐项比对；GroupBy 按 key 首次出现顺序返回，不依赖 map 遍历。中途与全部算子执行后显式 GC，复查输入与结果中的指针身份和 blob；最后在 `[]*string` 上二次实例化 Map/Filter。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_slice_ops`；
2. Filter 保持输入相对顺序，Distinct 保留 key 首次出现元素，GroupBy 的分组顺序与组内顺序均可确定复现；
3. Map/Reduce/FlatMap/Zip 的结果长度、值、指针与独立复算一致；Chunk 覆盖所有元素且最后一块可短；
4. 越界/空展开路径（FlatMap 返回 nil）不 panic，空展开不计入结果；
5. 显式 GC 后输入和结果中的负载指针不变、blob 完好；string 实例的 Map/Filter 行为正确；
6. 在 clobberfree 等 profile 下结果一致。

# 失败判读

- `... order/pointer mismatch`：泛型函数实例化后切片元素搬运错误或别名问题；
- `GroupBy key order mismatch`：实现退化为 map 遍历导致非确定顺序；
- `length mismatch`：append/切片边界错误；
- `blob corrupted` 或 bad pointer：结果切片未正确保活输入指针，怀疑栈映射/写屏障；
- 超时：正常应秒级完成，超时多为 GC 饥饿。

## 参数

- `UAF_SEED`：决定输入规模（260–320）与洗牌顺序。

## 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_slice_ops/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
