---
id: l01_generic_queue_stack
dim: L01_generics
priority: P0
sources:
  - kitex@v0.13.0: pkg/remote/trans/ttstream/container/queue_test.go:26 NewQueue[int] 生产/消费与 Size 归零
  - kitex@v0.13.0: pkg/remote/trans/ttstream/container/stack_test.go:26 NewStack[int] 交替 Pop/PopBottom 消费
  - kitex@v0.13.0: pkg/remote/trans/ttstream/container/stack_test.go:88 RangeDelete 回调删偶数后 Size 恰减半
tags: [L01, generics, queue, stack, ring-buffer, callback, pointer-identity, gc]
requires: [generics]
runtime: "<3s"
---

# 场景

以 kitex `ttstream/container` 的泛型队列/栈为骨架：`Queue[T]` 用环形缓冲实现 FIFO，入队到一半和满时触发扩容；`Stack[T]` 提供 `Push/Pop/PopBottom/RangeDelete`。元素均为带 `blob []byte` 的 `*payload`，全局 sink 保活，跨多轮显式 GC 校验出队/出栈指针身份与内容；`RangeDelete` 删除偶数后校验剩余集合与总和，GC 后再遍历一次。另以 `Queue[int64]` 验证不同实例化之间状态隔离。

# 期望行为

1. 队列入队 n 个元素后 Size==n，出队顺序严格 FIFO，返回指针与入队时同一地址，`id/name/blob` 在 GC 后不变；
2. 队列排空后 Size==0，空队列 Get 返回 ok=false；GC 后再入队触发扩容，新旧指针身份均保持；
3. 栈 Pop 严格 LIFO，PopBottom 严格由底向上，均返回原始指针；
4. `RangeDelete` 删偶数返回 n/2，栈 Size==n/2，剩余全为奇数且总和等于奇数序列和，GC 后复查一致；
5. `Queue[int64]` 实例化不受 `Queue[payload]` 影响。

# 失败判读

- `FAIL: ... pointer ...`：栈/队列中保存的指针被 GC 破坏或扩容拷贝漏元素，优先怀疑栈映射/写屏障；
- `FAIL: ... payload/blob corrupted`：元素内存被回收或零化，检查指针是否真正可达；
- panic 或死循环：环形索引 `(head+size)%len(buf)` 在扩容路径上失配。

# 参数

- `UAF_SEED`：元素数量 n=240+seed%16（取偶），失败输出带种子；不改变期望结果；
- 无 TORTURE 参数（P0 快速路径）。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_queue_stack/*.go
UAF_SEED=7 GODEBUG=clobberfree=1 /tmp/case
```
