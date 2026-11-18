---
id: l09_cgo_alloc_failure
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库分配/生命周期）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，裸 C 内存管理）
tags: [L09, cgo, posix_memalign, EINVAL, ENOMEM, calloc-overflow, realloc, C-heap, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定层对 C 分配失败路径的防御：`posix_memalign` 覆盖非法对齐（0/1/2/3/4/6/12/24，返回 EINVAL 且不写出参）与合法对齐（8/16/32/64/4096，按请求对齐）；`malloc(SIZE_MAX)`、`calloc(SIZE_MAX/4+1,8)` 乘法溢出、`realloc(p,SIZE_MAX)` 返回 NULL+ENOMEM，且失败的 realloc 必须保持原块 64 字节内容；另验证 `realloc(NULL,n)` 等价 malloc、缩小保留前缀、calloc 零初始化与 free(NULL)。所有块在 C 堆，跨显式 GC 用哨兵字节校验，C 不保留 Go 指针。

# 期望行为

1. `EINVAL` 与 `ENOMEM` 非零且互不相等，`alignof(max_align_t)>=8`；
2. 8 种非法对齐的 `posix_memalign` 均返回 EINVAL，且出参保持 NULL；
3. 5 种合法对齐 + 100 字节的分配返回 0，指针按请求对齐，填充 0xa5 后 C 校验通过，GC 后内容不变；
4. `posix_memalign(64, SIZE_MAX-4095)` 返回 ENOMEM 且出参 NULL；
5. `malloc(SIZE_MAX)` 与 `calloc(SIZE_MAX/4+1, 8)` 均返回 NULL 且 errno=ENOMEM；
6. 64 字节块填充 0x5a 后 `realloc(p,SIZE_MAX)` 返回 NULL+ENOMEM，原块 64 字节仍全部为 0x5a；
7. `realloc(NULL,128)` 得到可写块，填充 0x33 后缩小到 32 字节，前 32 字节保持 0x33；
8. `calloc(16,4)` 返回的 64 字节全部为零，可写后校验通过；`free(NULL)` 不崩溃；
9. 200 轮“失败巨块 + 成功小块”交替分配（每 25 轮显式 GC）均符合预期；退出码 0，最后一行 `ALL PASS l09_cgo_alloc_failure`。

# 失败判读

- 非法对齐返回 0 或非 EINVAL：`posix_memalign` 包装/参数类型错误；
- 失败出参残留旧值：未按 POSIX “失败时 memptr 不修改”先置 NULL，Go 侧会读到脏指针；
- 巨块分配成功：环境允许超量 overcommit，属于环境异常，不应继续使用该指针；
- 失败 realloc 后原块内容变化：实现未遵守“失败时原块不变”，或 Go 侧误用了返回值；
- 小块失败或 ENOMEM 缺失：errno 未在同一次调用内捕获；
- 崩溃：对 NULL 解引用、重复 free 或把 Go 指针交给 C 长期持有。

# 参数

- 无随机路径；循环固定 200 轮，GC 每 25 轮一次；巨块请求使用 `SIZE_MAX` 级尺寸，不实际占用内存；`UAF_SEED` 仅由 runner 注入。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_alloc_failure
go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
