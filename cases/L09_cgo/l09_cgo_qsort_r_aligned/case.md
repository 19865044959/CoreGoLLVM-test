---
id: l09_cgo_qsort_r_aligned
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:46（MinIO 覆盖 L09，C 结构体数组是数据面边界）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，C 侧批量重排/对齐访问）
  - docs/RUNBOOK.md:37（L09 依赖 cgo 能力标签与 C 工具链）
tags: [L09, cgo, qsort_r, posix_memalign, struct-array, alignment, context, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 数据面把定长记录数组按调用方传入的上下文排序：`qa_rec_t{key,tie,payload,pad}` 数组由 `posix_memalign` 按 64 字节对齐分配，C 比较器通过 glibc `qsort_r` 的 `arg` 接收 `qa_ctx_t`，按 `mode` 选择 key 升序/降序并在同 key 内按唯一 `tie` 升序，同时统计比较次数与元素地址是否仍 4 字节对齐。Go 只校验 sizeof/offsetof、排序顺序、payload 派生值、tie 双射与 64 字节对齐；数组全程在 C 堆，C 不保留 Go 指针。

# 期望行为

1. `sizeof(qa_rec_t)`、`alignof(qa_rec_t)`、`offsetof(key/tie/payload)` 与 Go 的 `unsafe.Sizeof/Alignof/Offsetof` 一致；`sizeof(qa_ctx_t)` 一致；
2. `posix_memalign` 返回 0，数组基址满足 64 字节对齐；n=1/2/7/64/257 均成立；
3. 每个 n 下 mode=0 得到 key 升序、tie 升序，mode=1 得到 key 降序、tie 升序，`qa_verify` 返回 -1；
4. 排序后 tie 是 `[0,n)` 的双射，每条记录 `key=(tie*13+5)%11`、`payload=tie*7+1`、`pad=0`；
5. 比较器观察到的元素地址全部 4 字节对齐（`misaligned==0`），n>1 时比较次数大于 0；
6. 对已排序数组再次排序结果不变；每个阶段之间显式 GC 不影响 C 堆内容。

# 失败判读

- `sizeof/offsetof` 失败：Go/C 对结构体布局的理解不一致，检查编译器结构体布局或 cgo 类型映射；
- 顺序断言失败但 `qa_verify` 通过：Go 侧期望计算错误；两者同时失败：比较器 mode/key/tie 逻辑或 qsort_r 调用约定问题；
- `comparator misaligned`：C 传入的比较元素地址未按记录对齐，疑似 qsort 按字节移动的调用约定/ABI 问题；
- `posix_memalign` 非 0：C 堆分配异常或对齐参数非法；
- 崩溃/`SIGSEGV`：C 堆被越界写，按失败信息中的 n/mode 缩小范围。

# 参数

- 无随机路径，固定覆盖 n=1/2/7/64/257 与 mode 0/1；`UAF_SEED` 仅由 runner 注入，不参与逻辑。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_qsort_r_aligned
go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
