---
id: l09_cgo_go_slice_to_c
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09 等维度，C 写 Go 数值缓冲是典型路径）
tags: [L09, cgo, go-slice, sentinel, pointer-rules]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 库“由 C 填充 Go 数值切片”的边界：把 `&goSlice[1]` 传给 C 写中间 6 个元素，首尾哨兵检测越界写；C 只读 Go `[]int64` 求和；C 原地缩放 Go 切片 64 轮，每轮 GC 后由 Go 读回。C 全程不保留 Go 指针。

# 期望行为

1. C 写入的 6 个 int32 等于 `1000+i*3`，`base[0]`/`base[7]` 哨兵保持 999；
2. GC 后写入值不变；
3. C 读取 Go int64 切片求和正确，nil + n=0 返回 0；
4. 64 轮 C 原地缩放 + GC 后每个元素等于 `(i+1)*2`；
5. 最后一行 `ALL PASS l09_cgo_go_slice_to_c`。

# 失败判读

- 哨兵被改：C 写越界（n 或起始指针错误）；
- 子切片写入位置错：`&sub[0]` 指针转换错误；
- GC 后值变化/崩溃：Go 指针被 C 保留越过调用期，或切片未被保持存活；
- `cgo argument has Go pointer to Go pointer`：向 C 传递了包含指针的 Go 内存（本用例仅传数值切片）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_go_slice_to_c
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
