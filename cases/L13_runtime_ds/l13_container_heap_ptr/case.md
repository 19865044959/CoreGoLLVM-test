---
id: l13_container_heap_ptr
dim: L13_runtime_ds
priority: P1
sources:
  - etcd@v3.5.0: server/lease/lease_queue_test.go:22 TestLeaseQueue 倒序注册 50 个租约，按到期时间最小堆 Poll（:43）
  - badger@v4.5.0: table/merge_iterator_test.go:162 4 路有序迭代器用堆归并，重复 key 取舍确定
tags: [L13, container/heap, priority-queue, fix, remove, pointer, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

`container/heap` 指针元素（租约/定时任务/归并场景）：

1. 1500 个 `*task` 逐个 `heap.Push`，优先级取自 `UAF_SEED`；每 300 次校验堆序并 GC；全部 `heap.Pop` 的顺序必须与独立 `sort.Slice` 参考全序逐指针一致；
2. `heap.Fix`：修改中间元素优先级后修复，另行 Push 一个极端元素，再全量弹出对比参考；
3. `heap.Remove`：删除 index=5 的元素，校验返回值、长度、被删元素不再出现，剩余全序正确；
4. 大顶堆：600 项按优先级降序弹出，与 max 参考一致；
5. 30 轮分批 churn（每轮 Push 100、Pop 50），每 10 轮 GC，校验堆序、剩余元素内容与弹出顺序；
6. 所有 task 含 name/pad，GC 后逐项校验指针与内容。

# 期望行为

1. Push/Pop 结果与参考排序逐指针一致，堆序不变量始终成立；
2. Fix/Remove 后长度、内容和弹出顺序正确；
3. 大顶堆顺序为优先级降序；
4. 分批 churn 不丢元素、不重复、不复活已弹出指针；
5. GC 前后 task 内容与堆结构有效；
6. 最后一行 `ALL PASS l13_container_heap_ptr`。

# 失败判读

- `invariant broken`：Push/Pop/Fix/Remove 后父子关系不满足堆序；
- `pop order mismatch`：堆实现丢元素、重复元素或比较函数错误；
- `remove returned wrong`：Remove 后交换/下沉路径错误；
- `payload corrupt` / GC 后崩溃：堆底层 `[]*task` 指针未被扫描（UAF）；
- `drain length`：churn 中元素数量维护错误。

# 参数

- `UAF_SEED`：各 task 优先级与内容种子，失败输出带该值。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_container_heap_ptr/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
