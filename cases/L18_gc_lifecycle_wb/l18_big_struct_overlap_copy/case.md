---
id: l18_big_struct_overlap_copy
dim: L18_gc_lifecycle_wb
priority: P2
sources:
  - nats-server@v2.11.0: server/avl/norace_test.go:33（TestNoRaceSeqSetSizeComparison：大数组的堆占用与 GC 观测）
  - badger@v4.5.0: y/y_test.go:301（TestAllocatorReuse：Allocator Reset 后 1M 次 Copy/append 重用内存）
tags: [L18, large-object, struct-copy, overlapping-copy, zeroing, write-barrier, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 2 轮；TORTURE_ROUNDS 放大轮数，上限 12）"
---

# 场景

每轮 noinline `bsAlloc` 分配 4 个大于 32KB 的 `bsHuge`（指针、interface、string、8 个 string tag、3600×int64、4KB 字节数组）：

1. 新分配后逐字段校验全零；
2. noinline `bsFill` 写入年轻节点与确定性模式，GC 后深度校验；
3. noinline `bsCopyForward` 执行重叠 `copy(arr[0:3], arr[1:4])`，GC 后校验 `[b1,b2,b3,b3]` 与内部指针/接口/字符串/大数组；
4. noinline `bsClear` 用 `*p = bsHuge{}` 清零尾槽，GC 后确认所有字段归零且前槽仍引用 b3；
5. noinline `bsCopyBackward` 执行反向重叠拷贝，GC 后校验 `[b1,b1,b2,b3]`。

# 期望行为

1. 大 struct 新分配全零（含 interface/string/tags/大数组/字节缓冲）；
2. 重叠拷贝后每个槽位的节点、interface 别名、string、tags、arr、buf 全部正确；
3. `bsHuge{}` 清零后无残留指针/字符串，GC 不把清零槽的旧字段当作存活根；
4. 仅被相邻槽引用的内部节点在 GC 后存活；
5. 失败信息带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_big_struct_overlap_copy`。

# 失败判读

- `fresh ... not zeroed`：大对象 span 复用未清零；
- `copy-forward/copy-backward ... corrupted`：重叠 typedmemmove 漏写屏障或指针字段未随 struct 搬移；
- `clear-tail ... not zeroed`：整 struct 清零遗漏字段，旧指针可能泄漏；
- `survivor ... corrupted`：仅由相邻大对象引用的内部节点被误回收。

# 参数

- `UAF_SEED`：版本基址与数据模式种子；
- `TORTURE_ROUNDS`：轮数（默认 2，上限 12；每轮约 4×33KB 填充 + 两次重叠拷贝）。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_big_struct_overlap_copy/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=8 UAF_SEED=2 /tmp/case
```
