---
id: l07_slice_header_lencap_ops
dim: L07_unsafe_zerocopy
priority: P0
sources:
  - fasthttp@v1.59.0: header_test.go:325 TestRequestRawHeaders 返回底层缓冲子切片，切片头/容量共享
  - sonic@v1.13.2: ast/node_test.go:137 TestLoadAll 惰性节点保存输入子切片，依赖切片头生命周期
  - pgx@v5.7.4: pgtype/bytea_test.go:86 TestPreallocBytes 小结果保留原 cap、len 改写
tags: [L07, unsafe, slice-header, len-cap, three-index, reslice]
requires: []
runtime: "<3s"
---

# 场景

手工构造 `{data unsafe.Pointer; len, cap int}` 切片头，在 `new([40]slot)` 堆数组上显式指定 len/cap，覆盖 header 回读、`unsafe.Slice` 等价性、len 在 cap 内伸缩、三索引切片钉死 cap、cap 内 append 原地写底层数组、满 cap append 重新分配、header 复制、尾部零长视图与跨 GC 保活。元素含 string 与 `*lnode`，用于验证 header 派生切片的扫描。

# 期望行为

1. 手工 header 的 len/cap 精确，首元素地址等于数组基址，第 k 个元素地址等于基址 + k*Sizeof；
2. header 回读与 `unsafe.Slice(base, cap)` 的 data/len/cap 一致；
3. 丢弃数组变量、仅由 header 构造的切片保活，GC 后所有 len 内元素及 link 正确；
4. `view[:cap]` 扩展与 `view[:len]` 收缩不改变 data 指针；
5. 三索引 `view[l-2:l:l+2]` 的 len=2、cap=4；cap 内 append 原地写入底层数组 l、l+1；越界 append 重新分配且不改写底层数组 l+2；
6. 满 cap append 重新分配，底层数组 cap 之后位置不变；
7. header 复制后的切片与原切片同 data/len/cap，跨 GC 后元素正确；
8. 尾部零长视图的 len=0、cap=cap-len，append 落在剩余 cap 内并原地写入；
9. 丢弃 view/raw 后，仅保留复制 header 切片与 unsafe.Slice 切片，GC 后 5 个追加元素与 l 之后区域全部正确；
10. 最后一行 `ALL PASS l07_slice_header_lencap_ops`。

# 失败判读

- len/cap 与预期不符：切片头布局/计算错误，或三索引 cap 计算错误；
- cap 内 append 未写到底层数组：cap 被错误当成 len，或底层数组被复制；
- 满 cap append 改写了源数组：append 的容量判断错误（越界写）；
- GC 后元素/link 错乱或 efence 崩溃：header 派生切片未被当作根，或元素扫描位图错误；
- 仅在 stress/k8s 失败：GOGC=1 下 header 构造的切片在并发标记中丢失。

# 参数

- `UAF_SEED`：决定初始 len（10..13）与 cap（len+6..len+9）、元素下标及追加值，失败信息带种子；
- 切片头字段布局按 64 位目标；本仓库按 linux/amd64、linux/arm64 运行。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_slice_header_lencap_ops/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
