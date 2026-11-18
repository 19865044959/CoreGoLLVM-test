---
id: l13_slice_filter_inplace
dim: L13_runtime_ds
priority: P2
sources:
  - goccy-json@v0.10.5: decode_test.go:3128 TestSliceElemUnmarshaler 复用 slice 先解 5 个再解 1 个，旧尾部应释放
  - go-redis@v9.8.0: internal/proto/scan_test.go:25 ScanSlice 同时支持 []T 与 []*T 目标
tags: [L13, slice, in-place-filter, compaction, zero-tail, dedupe, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s（默认 3 轮；TORTURE_ROUNDS 可放大）"
---

# 场景

服务端常见的“原地过滤/压缩”模式：写指针前移覆盖 `[]*elem`，过滤后把尾部旧指针清零再重新切片，避免底层数组继续保活已删除对象。每轮（默认 3 轮）用 `UAF_SEED` 生成 1400..1799 个带 payload/校验和的 `*elem`，覆盖：

1. 保留 `id%3!=0 && id%7!=0` 并核对顺序；尾部必须为 nil；过滤后 append 新元素并 GC；
2. 谓词恒真：指针身份必须逐项不变；
3. 谓词恒假：长度 0 且全部尾部清零；
4. 带 10 元素偏移的子切片过滤（与父切片共享底层数组）；
5. 纯 int 切片同样过滤；
6. `[][]byte` 行过滤，尾部置 nil，GC 后逐行内容与参考一致；
7. 去重（保留首次出现），用 O(n²) 前向扫描作为独立参考，校验指针身份。

每个变体之间都执行显式 GC + 垃圾分配。

# 期望行为

1. 各过滤结果的长度、顺序、id、name、payload 长度与校验和与参考实现一致；
2. 过滤后的尾部 `[len:oldLen)` 全部为 nil，GC 后仍为 nil；
3. 过滤后 append 不复活已丢弃元素，新元素跨 GC 有效；
4. 恒真谓词下 `filtered[i] == orig[i]`（同一 `*elem`）；
5. 恒假谓词下 len==0 且原切片全部位置为 nil；
6. 偏移子切片过滤结果与“全量过滤后取 id>=10”的参考一致；
7. `[][]byte` 保留行在 GC 后内容逐字节一致；
8. 去重结果与 O(n²) 参考的指针序列完全一致，GC 后有效；
9. 最后一行 `ALL PASS l13_slice_filter_inplace`。

# 失败判读

- 过滤结果顺序/id 错：写指针前移逻辑错误；
- 尾部非 nil：未清零被丢弃的指针，导致对象泄漏（本用例按“应清零”判定）；
- append 后旧元素复活或新元素内容丢失：尾部清零/重新切片边界错误；
- GC 后 payload 校验和变化或崩溃：切片元素指针未被扫描/保活（UAF）；
- 偏移子切片结果与父切片不一致：共享底层数组的边界计算错误。

# 参数

- `UAF_SEED`：每轮元素数量、payload 内容的种子，失败输出带该种子；
- `TORTURE_ROUNDS`：轮数，默认 3，范围 1..200。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_slice_filter_inplace/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_ROUNDS=50 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
