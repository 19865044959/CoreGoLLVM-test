---
id: l18_old_chain_splice_rotate
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - badger@v4.5.0: skl/skl_test.go:132（TestConcurrentBasic：并发 Put 不断把新节点挂入老 skiplist 链）
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1493（TestUnsafeAssumptions：多轮 GC 后老视图仍持有对象内存）
tags: [L18, write-barrier, old-chain, doubly-linked-list, noinline, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

先构造 48 个节点的双向链并经两轮 GC 整体晋升为老对象。随后 12 轮：

1. 由 `UAF_SEED` 选一个 base 节点 id，noinline `spliceYoung` 在其后插入一个年轻 splice 节点（老 anchor→年轻节点，并修复后继的 prev）；
2. noinline `appendYoungChain` 在老 `tail` 后追加 年轻 a→b→c 三节点链，并更新老 holder 的 tail 指针；
3. 立即 `runtime.GC()`×2，从 head 全量遍历，逐节点校验 next/prev 双向一致性、节点 id/name、长度与 tail 指针。

# 期望行为

1. 每轮 GC 后链长度为 `48 + 4*round`，splice 节点数恰为轮数且 id 为 `1001..1000+round`；
2. 每轮 young tail 链恰好为本轮新建的 `2000+round*10 .. +2`，且 `holder.tail` 指向 c；
3. 全链 prev/next 双向一致，48 个 base 节点一个不少、无重复；
4. 老 anchor 上写入的年轻 splice、老 tail 上追加的年轻链在 GC 后全部存活；
5. 失败输出带 `seed=<UAF_SEED>`，同种子可复现插入位置；
6. 退出码 0，最后一行 `ALL PASS l18_old_chain_splice_rotate`。

# 失败判读

- `old->... splice node id unexpected` / `tail chain ids ...`: 老链写入年轻指针时漏写屏障，年轻节点被回收或内存被复用；
- `prev link broken` / `tail pointer mismatch`: 双向链修复逻辑错误，或节点内存被破坏；
- `base node N lost` / `chain length`: 老节点被误回收或链断裂；
- clobberfree 下 name 变 0xde 垃圾、efence 下 SIGSEGV：确认 UAF；
- 失败时用 `UAF_SEED=<seed>` 复现选中的插入位置。

# 参数

- `UAF_SEED`：每轮选择 base anchor 的种子（默认 1；runner 每次注入不同值），只影响位置不影响成功输出；
- 固定参数：48 个 base、12 轮、每轮 1 个 splice + 3 个 tail 节点。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_old_chain_splice_rotate/main.go
UAF_SEED=7 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
