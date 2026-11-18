---
id: l09_cgo_realloc_array
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 内存生命周期）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，裸 C 内存管理）
tags: [L09, cgo, realloc, array-growth, alignment, C-heap, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定层用 `realloc` 维护 C 数组：`realloc(NULL,n)` 当首次分配、增长保留前缀、同尺寸不动内容、缩小保留前缀，并反复增长/收缩 64 轮；覆盖 8→32→13→1000→65536 个 int32 的尺寸变化，新扩展区由 C 填充后 Go 才读取。每步校验 `max_align_t` 对齐，期间显式 GC。内存全部归 C 堆，C 不保留任何 Go 指针。

# 期望行为

1. `alignof(max_align_t)>=8`，每次 realloc 返回的指针都满足该对齐；
2. `realloc(NULL,8)` 得到可用数组，C 填充后 Go 读回与公式 `i*3+1` 一致；
3. 增长到 32：前 8 个元素保留，新区域填充后全部正确，C 求和等于公式和；
4. 同尺寸 realloc 后 32 个元素不变；
5. 缩小到 13 后前缀 13 个元素不变；
6. 增长到 1000 与 65536（256 KiB）时前缀保留，新区域填充后全部正确；
7. 64 轮“增 17 个元素/每 8 轮缩到 13”循环中，每轮前缀、C 求和与对齐正确，周期 GC 不影响内容；
8. 最终 GC 后最后一块仍可读写，随后安全释放，`free(NULL)` 无副作用；
9. 最后一行 `ALL PASS l09_cgo_realloc_array`。

# 失败判读

- `realloc returned NULL`：C 堆耗尽或长度计算溢出；
- `realloc alignment`：C 分配器对齐保证被破坏或指针被截断；
- `prefix[...]`：缩小/增长丢失前缀，或写越界；
- `sum`：C 填充公式与 Go 期望不一致，或元素类型宽度错误；
- GC 后值变化/崩溃：Go GC 误管理 C 指针，或 realloc 后仍使用旧指针（本用例每步更新 Go 侧指针）；
- 崩溃：对已 free 的块继续访问（本用例末尾统一释放）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`；Linux/glibc。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_realloc_array
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
