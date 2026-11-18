---
id: l07_slice_header_overlap_exchange
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - fasthttp@v1.59.0: http_test.go:456 SwapBody 在池化对象间交换缓冲区所有权
  - pgx@v5.7.4: pgtype/bytea_test.go:86 PreallocBytes 保留原 cap、只改写 len
  - sonic@v1.13.2: ast/node_test.go:137 惰性节点保存输入子切片，依赖切片头生命周期
tags: [L07, unsafe, slice-header, overlapping-views, cap-append, pointer-swap, GC]
requires: []
runtime: "<10s"
---

# 场景

重叠窗口的 slice header 交换：同一 64 元素底层数组上取两个不相交窗口
`a[2:10:14]` 与 `b[14:22:26]`（len 8 / cap 12），以及跨两窗口的观察者 `a[6:26]`。
手工读写 `{data,len,cap}` 头交换两个窗口，再在各自 cap 内 append：A 的追加落在
`backing[22..23]`，B 的追加落在 `backing[10..13]`，观察者必须按物理偏移看到这些落点。
恢复 header、丢弃数组变量、仅保留 header 副本后跨 GC 重建窗口。模拟 fasthttp `SwapBody`
与 pgx 预分配缓冲的 len/cap 改写。

## 期望行为

1. 交换后两窗口的 data/len/cap 完全互换，元素内容随指针转移到对方窗口；
2. 交换本身不移动数据，观察者在追加前仍看到原始 id 序列；
3. A 在 cap 内追加 2 条后 data 指针不变，观察者 `obs[22..23]`、`mid[16..17]` 同步更新；
4. B 追加到满 cap 后 `obs[10..13]`、`mid[4..7]` 同步更新，相邻未追加槽位不受影响；
5. 恢复 header 后原 len 内元素完好，追加区只存在于 cap 之后；
6. 丢弃数组与窗口变量、显式 GC 后，观察者内容与 A 头地址关系仍正确；
7. 只保留 header 副本（unsafe.Pointer 根）并再次 GC 后，重建设窗口及 cap 区追加内容正确；
8. 末行 `ALL PASS l07_slice_header_overlap_exchange`，失败退出 1。

## 失败判读

- 追加落点错：header 交换后 cap 计算或三索引切片错误；
- 观察者不可见：append 走了重新分配而非原地写 cap 区；
- 恢复后数据错乱：交换期间底层数组被意外搬移；
- GC 后崩溃：手工 header 的 data 指针未被当作根，或 header 副本被优化掉；
- 仅 clobberfree/stress 失败：cap 区未初始化槽位被零化后仍被读取。

## 参数

- `UAF_SEED`：决定记录 name/link/data 的值模式，失败信息带种子。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_slice_header_overlap_exchange/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
