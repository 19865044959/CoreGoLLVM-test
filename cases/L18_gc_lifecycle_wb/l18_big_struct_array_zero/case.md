---
id: l18_big_struct_array_zero
dim: L18_gc_lifecycle_wb
priority: P2
sources:
  - badger@v4.5.0: skl/skl_test.go:162（100 goroutine 各写 1MB value 到 120MB arena）
  - nats-server@v2.11.0: server/avl/norace_test.go:33（500 万元素数组内存与 GC 观测）
tags: [L18, large-object, zeroing, clear, struct-array, write-barrier, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 2 轮；TORTURE_ROUNDS 放大轮数，上限 16）"
---

# 场景

每轮通过 noinline `allocBig` 分配 8192 个 `bigrec`（约 512KB，属于大对象），覆盖：

1. 新分配数组逐元素检查全零（含指针、string、字节数组）；
2. noinline `fillBig` 分配年轻 `*node` 写入全量槽位，多轮 GC 后逐元素校验 id/ver/name/指针/data；
3. `clear(arr)` 清零老数组后再 GC，逐元素确认无残留指针/字符串/数据；
4. 仅对偶数槽位再次 noinline 写入年轻对象，GC 后校验“半新半零”混合状态。

# 期望行为

1. 每轮新分配的大数组所有字段全零；
2. 全量写入后 GC 不丢失任何槽位的年轻对象；
3. `clear` 后所有槽位完全零化，数组不保留旧对象引用；
4. 半量重写后新槽位内容正确、零槽位保持全零；
5. 退出码 0，最后一行 `ALL PASS l18_big_struct_array_zero`。

# 失败判读

- `fresh large array slot N not zeroed`：大对象分配未清零（堆复用/零化缺失）；
- `slot N gen G corrupted`：老数组写年轻对象漏写屏障，或大对象内存被提前复用；
- `slot N not zeroed`：`clear` 未彻底清零，残留旧指针会造成内存泄漏或误存活；
- efence 下 SIGSEGV：确认访问已释放的大对象内存。

# 参数

- `TORTURE_ROUNDS`：轮数（默认 2，上限 16）；每轮分配约 512KB 大数组并做 3 次 GC 校验；
- 无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_big_struct_array_zero/main.go
TORTURE_ROUNDS=8 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
