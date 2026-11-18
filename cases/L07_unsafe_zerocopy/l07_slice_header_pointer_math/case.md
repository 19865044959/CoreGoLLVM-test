---
id: l07_slice_header_pointer_math
dim: L07_unsafe_zerocopy
priority: P0
sources:
  - docs/DIMENSIONS.md: L07 unsafe 与零拷贝（指针算术/别名分析）
tags: [L07, unsafe, pointer-arithmetic, slice-view, offset]
requires: []
runtime: "<3s"
---

# 场景

从结构体字段数组取类型化指针，构造全数组 slice 视图；用 `uintptr` 单表达式做偏移取槽位；取 string 字段的指针；保存元素槽位指针（`**node`）；校验 `Offsetof` 与实际布局一致。每步之间 GC。

# 期望行为

1. `(*[8]*node)` 视图与 `pa[:]` 在 GC 后 8 个元素全部正确；
2. `uintptr + 3*sizeof` 取到的 `arr[3]` 正确；
3. `*string` 字段指针在 GC 后内容正确；
4. `[]**node` 槽位指针数组在 GC 后仍指向有效元素；
5. `Offsetof` 与实际地址差一致；
6. 最后一行 `ALL PASS l07_slice_header_pointer_math`。

# 失败判读

- 视图/槽位指针失效：指针算术未按类型宽度，或 GC 未识别派生指针；
- 只有 clobberfree/efence 下失败：派生指针未使底层对象存活；
- 崩溃：`uintptr` 转换跨越安全点（用例中均为单表达式，若仍崩溃说明代码生成/栈映射异常）。

# 参数

无随机路径。指针算术依赖 `unsafe.Sizeof(*T) == 8`（64 位）；32 位平台需调整步长，本仓库按 linux/amd64、linux/arm64 运行。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_slice_header_pointer_math/*.go
GODEBUG=clobberfree=1 /tmp/case
```
