---
id: l09_cgo_fnptr_list
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，C 侧算子/回调分发）
  - docs/SOURCES.md:46（MinIO 覆盖 L09，C 结构体是数据面边界）
  - README.md:77（L09 允许 cgo 且需标注 requires）
tags: [L09, cgo, function-pointer, linked-list, struct, dispatch, recursion, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库把回调函数指针嵌在侵入式链表节点里的绑定形态：`fl_node{value,kind,pad,fn,next}` 由 C `calloc` 逐个分配并串成单链，C 沿 `next` 取每节点的 `fn` 执行 `acc=fn(node,acc)`。Go 在调用期通过 C 辅助函数替换头/中/尾节点的函数指针（幂等 id 选择 C 函数），校验迭代与递归遍历一致、整链结果、越界/非法 id 被拒绝、布局偏移，以及显式 GC 后指针链和函数指针仍稳定。C 只持有自己的内存和 C 函数指针，不保留 Go 指针。

# 期望行为

1. `sizeof(fl_node)` 与 `offsetof(value/kind/fn/next)` 的 Go/C 值一致；
2. n=1/2/8/63/257 建链后节点数、value=`i*17-40`、初始 fn id=`i%4` 全部正确，无空函数指针；
3. 初始状态下迭代与递归遍历在 seed=12345 和 seed=-7 上结果相同且等于 Go 按链序重算的值；
4. 依次替换头节点为 id3、尾节点为 id1、中点节点为 id2 后，结果仍等于 Go 重算值，递归与迭代一致；
5. 越界索引（n、-1）与非法 id 4 的替换返回 -1 且不改变链；
6. GC 后再次遍历结果不变，中点函数指针仍为 id2；
7. 进程退出码 0，最后一行 `ALL PASS l09_cgo_fnptr_list`。

# 失败判读

- `offsetof/sizeof` 失败：cgo 结构体布局映射与 C 不一致；
- 遍历结果不匹配：函数指针分发、`next` 指针链或函数 id 映射错误；
- 递归与迭代不一致：链在中途被破坏（next 悬空/成环）；
- `out-of-range/negative/invalid fn id` 未返回 -1：C 辅助函数的边界检查丢失；
- GC 后结果变化或崩溃：C 链表指针被 GC 误认为可达对象或内存被踩，检查是否误存 Go 指针。

# 参数

- 无随机路径；n 固定为 1/2/8/63/257，seed 固定 12345/-7；`UAF_SEED` 由 runner 注入但不参与逻辑。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_fnptr_list
go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
