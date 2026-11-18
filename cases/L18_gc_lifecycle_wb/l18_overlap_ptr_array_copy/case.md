---
id: l18_overlap_ptr_array_copy
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1493（TestUnsafeAssumptions：unsafe 视图跨多轮 GC 保持底层对象内存有效）
  - badger@v4.5.0: y/y_test.go:301（TestAllocatorReuse：Allocator Reset 后 1M 次 Copy/append 重用内存）
tags: [L18, write-barrier, overlapping-copy, pointer-array, interface-array, string-array, noinline, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

全局老 holder 内含三个长度 12 的定长数组：`[12]*opNode`、`[12]interface{}`、
`[12]string`。每轮 noinline `opFill` 先把三组数组整体重写为年轻代（指针、
interface 指向同一节点），GC 后再用 noinline `opCopyFwd`/`opCopyBwd` 执行
`copy(arr[0:11], arr[1:12])` 或反向重叠拷贝，GC 后校验别名序列、动态类型与
逐字节 payload。默认 4 轮，每轮前向+反向各一次，覆盖两种重叠方向与三种元素
类型的 bulkbarrier。

与 `big_struct_overlap_copy`（大 struct 值搬移）不同，本用例专门覆盖
指针/interface/string 数组元素级重叠拷贝的写屏障与别名保持。

## 期望行为

1. 前向重叠拷贝后序列为 `[n1,n2,...,n11,n11]`，反向为 `[n0,n0,n1,...,n10]`；
2. 每个槽位的指针指向期望节点，`ifc[i]` 与 `arr[i]` 保持同一对象别名；
3. `str[i]` 内容与期望代次/索引一致；
4. 所有被拷贝节点与 interface/string 数据跨 GC 完整；
5. 失败信息带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_overlap_ptr_array_copy`。

## 失败判读

- `arr[i] node corrupted`：重叠 typedmemmove 漏写屏障或拷贝顺序错误；
- `ifc[i] interface alias broken`：interface 数组批量搬移丢失 itab/指针；
- `str[i] content corrupted`：string 头拷贝错误或底层字节被回收；
- `data corrupted`：仅被数组引用的对象被提前回收。

## 参数

- `UAF_SEED`：数据模式与每轮先前向/先反向的选择；
- 无 TORTURE 参数；轮数固定为 4（每轮两阶段）。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_overlap_ptr_array_copy/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GODEBUG=clobberfree=1 /tmp/case
```
