---
id: l09_cgo_qsort_struct
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，C 侧排序/批量重排）
  - docs/SOURCES.md:46（MinIO 覆盖 L09，C 结构体数组是数据面边界）
tags: [L09, cgo, qsort, struct-array, export-callback, bsearch, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库对记录数组排序的绑定：`person_t{score, age, id, name[12]}` 数组由 C 填充，C qsort 在调用期内反复回调 Go 比较器（`//export goPersonCmp`，按 score 降序、age/name/id 升序），比较器偶尔分配垃圾并 `runtime.GC()`；另用按 score 全序的 Go 比较器驱动 C `bsearch`。score 由双射公式生成、互不重复，因此 qsort 的不稳定性不影响确定输出。C 不保留 Go 数组指针或函数指针到调用结束之后。

# 期望行为

1. `sizeof(person_t)` 与 `offsetof(name/age)` 的 Go/C 值一致；
2. 200 条记录经 C qsort 后与 Go `sort.Slice` 参考结果逐字段（score/age/id/name）一致，C 侧有序检查通过，比较器确实被调用；
3. C 求和等于 Go 参考求和；
4. bsearch 对 6 个已存在 score 返回正确位置与 id，对集合外 score 返回 -1；
5. n=1 不触发比较器，n=0 的 bsearch 返回 -1；
6. C `malloc` 拥有的 128 条记录数组走同一比较器路径，排序与参考一致，GC 后仍一致；
7. GC 后重新填充 64 条 Go slice 再排序，结果仍与参考一致；
8. 最后一行 `ALL PASS l09_cgo_qsort_struct`。

# 失败判读

- `sizeof/offsetof`：cgo 结构体布局翻译错误；
- `fill/score/age/name`：C 填充规则或 Go 读取字段偏移错误；
- `matches Go ...`：C 比较器方向/平局规则与 Go 参考不一致；
- `bsearch ...`：全序方向与排序方向不一致（本用例比较器为 score 降序）；
- GC 时崩溃：回调帧栈映射问题，或 C 保留了 Go 指针越过调用期；
- 构建失败：`//export` 所在文件 preamble 只能有声明，C 定义须留在 `qs.c`。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。用例断言 score 唯一，不依赖 qsort 稳定性。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_qsort_struct
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
