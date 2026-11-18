---
id: l09_cgo_c_sort
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，C 侧排序/批量内存重排场景）
  - docs/SOURCES.md:46（MinIO 覆盖 L09，C 结构体数组是常见数据面边界）
tags: [L09, cgo, c-sort, insertion-sort, merge-sort, stability, struct-array]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库自带的排序实现（不回调 Go）：`c_sort_people` 用稳定插入排序按 `score` 降序、`age` 升序重排 `person_t{id,age,score,name[12]}` 数组；`c_sort_i32_asc` 用归并排序重排 `int32` 数组。输入分别覆盖 Go slice（C 在调用期内直接改写）与 C `calloc` 数组；输出与 Go `sort.SliceStable`/`sort.Slice` 参考实现逐元素对照。C 不保留任何 Go 指针。

# 期望行为

1. C 填充的结构体数组与 Go 参考填充规则逐字段一致；
2. C 稳定排序结果与 Go `sort.SliceStable` 完全一致（含 name/id 字段）；
3. `score/age` 相同的记录保持原 `id` 递增顺序（稳定性）；
4. C 侧校验函数认为结果有序，C 校验和与 Go 参考一致；
5. GC 后排序结果仍正确；
6. 归并排序覆盖 n=1/2/3/64/257，含 `int32` 极值，结果与 Go `sort.Slice` 一致；
7. C 拥有的数组可独立 allocate/fill/sort/read/free；
8. 最后一行 `ALL PASS l09_cgo_c_sort`。

# 失败判读

- `fill layout`：cgo 结构体字段布局与 C 不一致；
- `matches Go SliceStable`：C 比较器方向/稳定性实现错误，或 Go 参考排序规则不一致；
- `stable tie`：插入排序在相同键时错误交换；
- `merge sort`：C 归并边界/极值处理错误，或 Go slice 越界写；
- GC 后结果变化/崩溃：C 误存 Go 指针（本用例排序函数只在调用期内访问输入）；
- 构建失败：`c_sort.c` 未参与 module 构建，或头文件声明不匹配。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_c_sort
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
