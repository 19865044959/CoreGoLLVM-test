---
id: l18_old_chain_two_hop
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - badger@v4.5.0: skl/skl_test.go:132（skiplist 多层节点把新插入节点挂到老节点链上）
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1493（多层视图对象跨 GC 存活）
tags: [L18, write-barrier, old-to-old-to-young, pointer-chain, noinline, gc]
requires: []
runtime: "<3s"
---

# 场景

先多轮 GC 把 holder、head、mid、alt 晋升为老对象。之后每轮用 noinline setter：

1. `setTail` 把年轻节点写入老 `mid.next`，形成 老（head）→ 老（mid）→ 年轻（tail）两级链；
2. `setAltChain` 把老 `alt.next` 换成一个全新年轻中间节点，该中间节点再指向年轻叶子，形成 老→年轻→年轻 链。

setter 返回后，新对象只被老对象链引用；每次写入后立即 `runtime.GC()`，再从老根遍历整条链校验。

# 期望行为

1. 每次 GC 后 `head→mid→tail` 两级链完整，tail 的 `v` 与轮次一致；
2. 每次 GC 后 `alt→m→leaf` 新链完整，m/leaf 的 `v` 分别为 `v`、`v+1`；
3. 24 轮稳定，老节点本身内容不漂移；
4. 退出码 0，最后一行 `ALL PASS l18_old_chain_two_hop`。

# 失败判读

- `old->old->young tail lost`：老对象两级链末尾写年轻指针时漏写屏障；
- `old->young middle lost` / `young->young leaf lost`：老根写年轻中间节点或年轻链内部指针被误回收；
- clobberfree 下读到 0xde 垃圾、efence 下 SIGSEGV：确认对象已被提前回收（UAF）；
- `chain head corrupted`/`head->mid link broken`：老对象自身被破坏，检查栈扫描或堆对象元数据。

# 参数

无随机路径；固定 24 轮。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_old_chain_two_hop/main.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
