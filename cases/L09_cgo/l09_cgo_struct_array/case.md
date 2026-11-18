---
id: l09_cgo_struct_array
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，C 结构体数组是常见 cgo 边界）
tags: [L09, cgo, struct-array, unsafe.Slice, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库返回结构体数组：C.calloc 分配 64 个 `item_t{id,pad,score,name[16]}` 并填充；Go 用 `unsafe.Slice` 直接遍历、改字段，C 侧求和立即观察修改；数组跨 64 轮 GC 不变，最后 free。

## 期望行为

1. `sizeof(item_t)` 与 `offsetof(name)` 的 Go/C 断言一致；
2. 64 个元素的 id/pad/score/name 全部正确；
3. Go 修改 score/id/pad 后 C 求和立即反映；
4. 64 轮 GC + 垃圾分配后 score、name、字段不变；
5. 最后一行 `ALL PASS l09_cgo_struct_array`。

## 失败判读

- 布局不一致：cgo 结构体映射或对齐假设错误；
- 名称乱码：C 未写结尾 NUL 或 Go 读取越界；
- GC 后内容变化/崩溃：C 内存被误纳入 Go 堆管理，或 Go 指针被 C 保留；
- 重复 free/漏 free：本用例只在末尾释放一次。

## 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=cgo` 可过滤本用例。

## 运行

```bash
cd cases/L09_cgo/l09_cgo_struct_array
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
