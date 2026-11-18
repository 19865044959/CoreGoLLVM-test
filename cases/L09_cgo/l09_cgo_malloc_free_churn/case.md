---
id: l09_cgo_malloc_free_churn
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库分配/释放生命周期）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，裸 C 内存管理边界）
tags: [L09, cgo, malloc, free, realloc, leak-balance, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定层的 C 内存高频分配/释放循环：200 轮里每轮 `malloc` 3 个瞬时块（16/257/4096 字节），每 7 轮对其中一个 `realloc` 放大并校验前缀保留；同时有 4 个长期存活块（33/129/1025/8193 字节）跨全程与多次显式 GC。C 侧维护 `allocs/frees/live_bytes` 统计，每轮释放瞬时块后 live 必须只剩长期块，最终全部释放后 alloc 次数等于 free 次数、live 为 0。所有块由 C 拥有，Go 只通过 `unsafe.Slice` 读写，C 不保留 Go 指针。

# 期望行为

1. 初始 allocator 统计全为 0；
2. 每轮瞬时块内容正确，释放后 `live_bytes` 等于长期块总大小，`allocs-frees` 等于 4；
3. `realloc` 后原 257 字节前缀保留，新扩展区域可写并校验；
4. 每 10 轮显式 GC 后长期块内容不变，allocator 统计不回退；
5. `free(NULL)` 不改变任何统计；
6. 释放全部长期块后 `live_bytes==0` 且 `allocs==frees`；
7. 最后一次 GC 后统计仍稳定；
8. 最后一行 `ALL PASS l09_cgo_malloc_free_churn`。

# 失败判读

- `malloc/realloc` 失败：C 堆耗尽或大小计算错误；
- `byte[...]`：块内容被越界写/提前释放（对应 UAF/overrun）；
- `live bytes`：free 时大小不符、重复释放或统计记账错误；
- `alloc/free balance`：泄漏（少 free）或 double free（多 free）；
- GC 后内容/统计变化：Go GC 误回收 C 指针（本用例 C 内存地址保存在 Go 堆的 `unsafe.Pointer` 中）；
- 崩溃：`realloc` 返回 NULL 后仍使用旧指针、或释放后继续访问。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_malloc_free_churn
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
