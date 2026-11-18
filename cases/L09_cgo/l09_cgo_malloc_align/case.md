---
id: l09_cgo_malloc_align
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:46（MinIO 覆盖 L09，C 数据面缓冲区/对齐访问）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库分配边界）
tags: [L09, cgo, posix_memalign, alignment, malloc, EINVAL, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定层按对齐要求分配 C 内存：`posix_memalign` 覆盖 8/16/32/64/128/256/1024/4096 对齐与 1B..64KiB 尺寸，C 填充确定性模式并由 Go 直接读回；另有 256 KiB/4 KiB 对齐大块、`malloc` 的 `max_align_t` 对齐、非法对齐（3/24/4/0）返回 EINVAL。所有块跨 12 轮显式 GC 校验对齐、首尾字节与 FNV 哈希。C 只持有自己分配的内存，不保留 Go 指针。

# 期望行为

1. 8 种对齐 × 5 种尺寸的 `posix_memalign` 全部成功，返回指针按请求对齐；
2. C 填充的模式与 Go 公式逐字节一致，C FNV 哈希等于 Go 参考哈希；
3. 256 KiB 大块按 4 KiB 对齐并可跨 GC 校验；
4. 非法对齐 3/24/4/0 返回 EINVAL 且不写指针；
5. `malloc` 返回的指针满足 `alignof(max_align_t)`；
6. 12 轮显式 GC 后每个块的对齐、首尾字节、哈希全部不变；
7. 所有块最终释放，`free(NULL)` 安全；
8. 最后一行 `ALL PASS l09_cgo_malloc_align`。

# 失败判读

- `posix_memalign ...`：对齐/尺寸非法或 C 堆耗尽；
- `alignment ...`：C 分配器返回未对齐指针，或 Go 读取的指针被截断；
- `first/last byte` / `C hash`：C 写入越界、Go 复算公式不一致；
- `invalid alignment`：未检查 `posix_memalign` 返回值（其失败不写指针）；
- GC 后哈希变化：C 指针被 Go GC 误管理（不应发生）或块被提前释放；
- 崩溃：重复 free 或访问已释放块（本用例末尾统一释放一次）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`；Linux/glibc（`posix_memalign`、`max_align_t`）。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_malloc_align
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
