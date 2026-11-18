---
id: l18_huge_struct_zero_assign
dim: L18_gc_lifecycle_wb
priority: P2
sources:
  - badger@v4.5.0: skl/skl_test.go:162（TestConcurrentBasicBigValues：1MB value 批量写入/读取 arena）
  - nats-server@v2.11.0: server/avl/norace_test.go:33（TestNoRaceSeqSetSizeComparison：百万元素级大对象分配与内存观测）
tags: [L18, large-object, zeroing, huge-struct, value-copy, clear, noinline, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 3 轮；TORTURE_ROUNDS 放大轮数，上限 24）"
---

# 场景

每轮通过 noinline `allocHuge` 分配一个约 33KB（>32KB 大对象阈值）的 `hugeRec`，含 *hnode、interface、string、4096×int64 和 1KB 字节数组：

1. 新分配对象必须全零（标量/指针/接口/数组）；
2. noinline `fillHuge` 写入当轮年轻节点与确定性模式，GC 后逐字段校验；
3. noinline `copyHuge` 做整 struct 值拷贝，GC 后校验目标数据完整且与源共享同一内部指针/接口；
4. noinline `resetHuge` 用 `*p = hugeRec{}` 清零，GC 后所有字段必须归零、不残留旧指针；
5. 下一轮重新分配，覆盖大对象 span 复用/零化路径。

# 期望行为

1. 每轮新分配的大 struct 全部字段为零；
2. 填充后即使内部节点仅被大对象引用，GC 后数据、指针、接口全部保留；
3. 值拷贝得到独立 struct，但指针/接口字段与源共享底层节点；
4. `*p = hugeRec{}` 后 GC 不残留任何旧指针/字符串/数组内容；
5. 退出码 0，最后一行 `ALL PASS l18_huge_struct_zero_assign`。

# 失败判读

- `fresh huge struct (round N) not zeroed`：大对象 span 复用未清零，会残留旧指针造成误存活/UAF；
- `filled/copied huge struct ... corrupted after GC`：大对象的指针字段漏扫描或写屏障缺失，节点被提前回收；
- `did not share pointer field` / `interface field corrupted`：struct 值拷贝语义错误（非 GC 问题）；
- `reset ... not zeroed`：清零被优化掉或写入不完整；
- efence 下 SIGSEGV：确认访问已释放的大对象内存。

# 参数

- `TORTURE_ROUNDS`：轮数（默认 3，上限 24）；每轮 1 次新分配 + 2 次 reset + 多次强制 GC；
- 无随机路径；`UAF_SEED` 不影响结果。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_huge_struct_zero_assign/main.go
TORTURE_ROUNDS=12 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
