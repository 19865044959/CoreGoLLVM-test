---
id: l09_cgo_malloc_gc_rounds
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo）
  - docs/SOURCES.md:49（containerd 覆盖 L09；C 堆对象跨 GC 是 cgo 持久化常见路径）
tags: [L09, cgo, malloc, calloc, GC-rounds, unsafe.Pointer]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定层长期持有 C 内存：`calloc` 分配 256 条 `rec_t` 记录，Go 用 `unsafe.Slice` 直接查看；另分配 8 块 4 KiB C 缓冲，把地址存入 Go 堆上的 `[]unsafe.Pointer`。随后 100 轮制造 Go 垃圾并 `runtime.GC()`，每轮校验 C 内存内容未变，最后统一 `free`。

# 期望行为

1. 初始化后 C 侧总和正确，抽查记录字段正确；
2. 100 轮 GC + 垃圾分配后，C 记录总和、单条记录字段、Go 视图、8 块 C 缓冲首字节全部不变；
3. 全部 C 内存最终显式释放，进程正常退出；
4. 最后一行 `ALL PASS l09_cgo_malloc_gc_rounds`。

# 失败判读

- GC 后 C 内容变化：C 内存被错误地纳入 Go 堆扫描/回收（不应发生）；
- 崩溃或 `invalid pointer found on stack`：Go 跨越调用期把 Go 指针交给 C，或 C 指针被误当 Go 指针；
- 总计不符：C 初始化/求和循环错误；
- 超时：循环过大（本用例固定 100 轮，P1 <5s）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。C 不保留任何 Go 指针。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_malloc_gc_rounds
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
