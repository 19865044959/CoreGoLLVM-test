---
id: l01_generic_heap_pq
dim: L01_generics
priority: P2
sources:
  - etcd@v3.5.0: server/lease/lease_queue_test.go:22 50 个租约按到期时间注册进最小堆，Poll 过期项后回填重试队列且不丢元素
  - badger@v4.5.0: table/merge_iterator_test.go:162 4 路有序迭代器正/反向归并，重复 key 保持稳定顺序
tags: [L01, generics, heap, priority-queue, indexed-heap, handle-update, k-way-merge, pointer-identity, gc, UAF_SEED, TORTURE_ROUNDS]
requires: [generics]
runtime: "<30s"
---

# 场景

自建泛型索引最小堆 `indexHeap[K cmp.Ordered, V any]`：`[]*heapItem` + `map[K]*heapItem` 句柄，支持 O(log n) 的 Push/Upsert/Remove/Pop/Get，等价于 etcd lease 到期队列的 RegisterOrUpdate 与 Poll 回填。另用泛型自定义比较器 `flexHeap[T]` 实现 badger 多路归并迭代器的正/反向 k 路归并（允许重复 key）。

UAF_SEED 驱动的 LCG 生成每轮 500 步 Push/Update/Remove/Pop/Get 序列，并与独立 map 引用模型逐步比对指针身份、旧值返回、最小 key 与长度；每 97 步显式 `runtime.GC()` 后校验堆序、idx/句柄一致性以及所有存活 payload 的 blob。最后分别实例化 `indexHeap[string,*pqPayload]` 与带重复 key 的归并堆。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_heap_pq`；
2. 每一步 Upsert 返回的 (旧指针, 是否已存在)、Remove/Pop 的存在性、key 与指针与引用模型完全一致；
3. 每次显式 GC 后堆序不变量、`idx` 与 `where` 句柄一致，存活 payload 的地址与 blob 内容不变；
4. 堆排空时严格按 key 升序弹出，模型同步清空；Pop 空堆返回 ok=false；
5. 正/反向 k 路归并结果与“拼接后排序”的参考序列逐元素一致（重复值按 list/pos 稳定）；
6. `indexHeap[string,*pqPayload]` 按字典序弹出且跨 GC 指针身份不变。

# 失败判读

- `upsert/remove/pop pointer mismatch`：句柄 map 与堆数组失步，或泛型值被复制后指针语义丢失；
- `heap invariant after GC` / `idx mismatch`：Remove 的 down/up 修复顺序错误或 GC 后节点被破坏；
- `merge order mismatch`：多路归并比较器或游标推进错误；
- `SIGSEGV` / `fatal error: found bad pointer`：大概率栈映射漏标或写屏障缺失；
- 超时：堆操作未收敛或 GC 饥饿。

# 参数

- `UAF_SEED`：决定随机操作序列、key 空间与字符串堆内容；失败输出带种子；
- `TORTURE_ROUNDS`：轮数（默认 3，P2 放大用）；
- `TORTURE_STEPS`：每轮随机步数（默认 500）。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_heap_pq/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=20 TORTURE_STEPS=4000 UAF_SEED=7 /tmp/case
```
