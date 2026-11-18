---
id: l01_slice_map_generic
dim: L01_generics
priority: P1
sources:
  - quic-go@v0.50.1: internal/utils/ringbuffer/ringbuffer_test.go:9 泛型 RingBuffer 的 Push/Peek/Pop、扩容回绕与 Clear
  - kitex@v0.13.0: pkg/remote/trans/ttstream/container/queue_test.go:26 泛型 Queue 容器持续 Add/Get 的指针搬运
  - pgx@v5.7.4: rows_test.go:335 CollectRows(rows, RowTo[int32]) 泛型收集器按顺序保存结果
tags: [L01, generics, slice-growth, map-container, pointer-elements, gc]
requires: [generics]
runtime: "<10s"
---

# 场景

自建泛型 `Vec[T]`（`[]*T` 追加、多次扩容）与 `GMap[K,V]`（`map[K]*V`，`int64`/`string` 两种键实例化），模拟 quic-go 泛型环缓冲、kitex 泛型队列与 pgx `CollectRows` 的指针收集路径。约 600–700 个带 `blob []byte` 的 item 指针写入容器，中途触发 GC，删除 1/7 条目，GC 后再追加 50 个元素触发二次扩容。

# 期望行为

1. slice 扩容前后元素顺序与指针身份不变，`blob` 内容/长度正确；
2. map 删除后 `Len` 精确减少、被删键不可见、存活键指针与 slice 中为同一地址；
3. 第二轮追加+GC 后旧元素仍有效，新元素顺序/内容正确；
4. `Cap() >= Len()` 成立；
5. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_slice_map_generic`。

# 失败判读

- `vec element pointer identity changed` / `old elements lost after growth+GC`：扩容时旧底层数组/指针被误回收，或写屏障漏标；
- `gmap pointer identity changed`：map bucket 里的指针未被扫描；
- `blob payload corrupted`：切片底层数组被复用时越界或提前回收；
- 输出中出现 panic（map/slice 内部结构损坏）：大概率是泛型实例化的栈映射问题。

# 参数

- `UAF_SEED`：决定初始元素数（600..696），失败输出带种子。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_slice_map_generic/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
