---
id: l13_slice_reslice_retention
dim: L13_runtime_ds
priority: P1
sources:
  - pgx@v5.7.4: pgtype/hstore_test.go:333 编码循环复用 `buf = buf[:0]`，依赖底层数组保留
  - badger@v4.5.0: stream_test.go:114 流式结果 `c.kv = c.kv[:0]` 清空后重新 Orchestrate
  - goccy/go-json@v0.10.5: decode_test.go:3128 TestSliceElemUnmarshaler 复用 slice 收缩长度后旧尾部处理
tags: [L13, slice, reslice, capacity-retention, full-slice-expression, aliasing, zero-tail, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

覆盖“收缩但保留底层数组”的切片语义：

1. `make([]*rec, 400)` 收缩为 `[:150]`，再重切回 `[:400]`：旧尾部指针必须原样可见；GC 后重复校验；
2. 容量内 append 一个元素：父切片 `master[150]` 立即可见，长度不变；GC 后元素仍在；
3. 三索引切片 `[:150:150]` 的 append 必须分配新数组，原数组位置与邻居不受影响；
4. 通过 `master[:cap(master)]` 把 `[150:400)` 全部置 nil，GC 后保持 nil，而 `[0:150)` 指针不变；
5. `copy`/`append([]*rec(nil), ...)` 只复制切片头：元素对象共享（修改 name 双端可见），替换元素不泄漏到原切片；
6. `s[:0]` 连续 24 轮复用同一底层数组填充 256 项，每 6 轮 GC 后逐项校验；再填满容量后 append 触发扩容，旧元素保留；
7. `[]string` 收缩/重切/满切片表达式 append，字符串内容不变且不写回父切片；
8. `UAF_SEED` 驱动 300 次随机收缩点，逐次校验 len/cap 与首元素身份，每 60 步 GC。

# 期望行为

1. 收缩后重切可恢复原尾部，指针身份与内容跨 GC 不变；
2. 容量内 append 写共享数组，三索引切片 append 不写共享数组；
3. 尾部清零后 GC 不再保留已删除元素，保留区间不受影响；
4. `s[:0]` 复用与扩容路径的元素内容和指针身份正确；
5. 字符串切片收缩/重切结果确定；
6. 最后一行 `ALL PASS l13_slice_reslice_retention`。

# 失败判读

- `regrow pointer mismatch`：reslice 长度/容量计算错误或底层数组被提前复用；
- `append did not write shared` / `full-slice append wrote`：append 是否扩容的判定错误；
- `tail not cleared`：尾部指针未清零，导致对象被底层数组继续保活；
- GC 后元素丢失或乱码：切片元素指针未被扫描（UAF）。

# 参数

- `UAF_SEED`：收缩点序列与元素内容种子，失败输出带该值。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_slice_reslice_retention/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
