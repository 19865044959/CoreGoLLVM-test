---
id: l07_slice_header_swap_transfer
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - fasthttp@v1.59.0: http_test.go:456 TestResponseSwapBodyConcurrent 池化对象间 Swap 交换缓冲区所有权
  - pgx@v5.7.4: pgtype/bytea_test.go:86 TestPreallocBytes 保留原 cap、改写 len
  - sonic@v1.13.2: ast/node_test.go:137 TestLoadAll 惰性节点保存输入子切片，依赖切片头生命周期
tags: [L07, unsafe, slice-header, swap, append-realloc, GC]
requires: []
runtime: "<10s"
---

# 场景

slice header 交换与所有权转移：把 `{data unsafe.Pointer; len,cap int}` 头在两个 `[]sRec`
之间整体交换、恢复，再交换后做 cap 内 append 与满 cap append。元素含 string、`*leaf` 与
[]byte。验证交换后的内容/长度/容量/数据指针全部随头转移，cap 内 append 原地写原底层数组
（旧 header 视图可见），满 cap append 重新分配且不影响旧数组；最后丢弃保存的 header 与切片
变量，只保留元素内部指针跨 GC。

## 期望行为

1. 从切片读出的 header 的 data/len/cap 与切片属性一致；
2. 交换后两个切片的 data 指针、len、cap、元素内容完全对调，保存的旧切片头仍指向原数组；
3. 恢复 header 后原切片完全复原；
4. 再次交换后对原 cap 内空间 append：cap/data 指针不变，旧 header 重切片可见新元素；
5. 对满 cap 切片 append：发生重分配，旧底层数组内容不被改写；
6. 丢弃旧 header 视图并 GC 后，两个切片的全部元素（含 string/leaf/data）正确；
7. 仅保留元素指针、丢弃切片头并 GC 后，元素内容仍正确且两个指针不同；
8. 末行 `ALL PASS l07_slice_header_swap_transfer`，失败退出 1。

## 失败判读

- 交换后 len/cap/data 不一致：header 结构布局或写回偏移错误；
- cap 内 append 未原地生效：旧 header 重切片未看到同一底层数组；
- 满 cap append 改写旧数组：重分配判断或旧 header 保活错误；
- GC 后内容错乱/崩溃：交换写回时写屏障缺失，或元素指针未被视作整数组根；
- 仅 clobberfree/efence 失败：旧底层数组被提前清零/回收。

## 参数

- `UAF_SEED`：决定元素值模式，失败信息带种子；
- 无 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_slice_header_swap_transfer/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
