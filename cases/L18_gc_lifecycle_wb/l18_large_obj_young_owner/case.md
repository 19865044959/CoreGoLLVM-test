---
id: l18_large_obj_young_owner
dim: L18_gc_lifecycle_wb
priority: P2
sources:
  - badger@v4.5.0: skl/skl_test.go:162（TestConcurrentBasicBigValues：100 goroutine 各写 1MB value 到大 arena）
  - nats-server@v2.11.0: server/avl/norace_test.go:33（TestNoRaceSeqSizeComparison：500 万元素数组的内存与 GC 观测）
tags: [L18, large-object, young-slice, old-interface, zeroing, noinline, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 4 轮；TORTURE_ROUNDS 上限 16）"
---

# 场景

每轮 noinline `loSet` 分配 4 个约 52KB 的大对象（>32KB，含 6000×int64、
指针/interface/string/tag/4KB 缓冲），先断言刚分配对象全零，再填充确定性数据，
装进全新年轻 slice 并写入已晋升 holder 的 interface 字段。按 `UAF_SEED` 决定
是否把上一代 slice 存入全局 retired；双 GC 后校验当前代全部字段，若保留则同时
校验上一代大对象仍存活且未被改写。

与既有大 struct 清零/重叠 copy 用例不同，本用例聚焦“老接口字段 → 年轻 slice
→ 大对象”的跨代持有链与大对象存活/零初始化边界。

## 期望行为

1. 每次新分配的大对象所有字段（含 48KB 数组、指针、interface、string、tag、buf）全零；
2. 当前代 4 个大对象的数组内容、节点、interface 动态类型、string/tag、buf 全部正确；
3. 写入老 holder interface 后，年轻 slice 与大对象跨 GC 存活；
4. retired 路径下上一代 slice 与大对象跨 GC 仍完整，且不与当前代共享对象；
5. 失败信息带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_large_obj_young_owner`。

## 失败判读

- `fresh large object not zeroed`：大对象 span 复用未清零；
- `large object ... data corrupted`：大对象被提前回收或内存复用；
- `interface ... corrupted`：老 interface 字段写屏障或年轻 slice 扫描错误；
- `retired ... corrupted`：上一代 slice 未被视为根，或全局 retired 写入丢失。

## 参数

- `UAF_SEED`：数据模式与 retired/丢弃路径选择；
- `TORTURE_ROUNDS`：轮数（默认 4，上限 16；每轮 4×约 52KB）。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_large_obj_young_owner/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=12 UAF_SEED=2 /tmp/case
```
