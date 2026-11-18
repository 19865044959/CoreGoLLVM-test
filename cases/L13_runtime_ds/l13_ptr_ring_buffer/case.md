---
id: l13_ptr_ring_buffer
dim: L13_runtime_ds
priority: P0
sources:
  - etcd@v3.5.0: raft/tracker/inflights_test.go:22 TestInflightsAdd 环形 buffer 的 start/count 回绕与满窗
  - sonic@v1.13.2: ast/buffer_test.go:42 Test_linkedPairs_Push 分块节点缓冲的 Push/Pop 重接
tags: [L13, ring-buffer, wrap, overwrite, grow, pointer, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

固定容量指针环形缓冲 `[]*node`（head/count）：满窗时覆盖最旧元素并前移 head，支持 popFront、扩容（保持逻辑顺序）、重置。用例覆盖：

1. 容量 16 连续添加 2000 个节点，逐次校验被覆盖指针 id == added-16；每 181 次添加显式 GC + 垃圾分配，并按 head 顺序校验全部 16 个存活节点的 name/pad；
2. 扩容到 64 后逻辑顺序不变；
3. popFront 10 次顺序正确，GC 后剩余元素有效；继续弹空，空环再弹返回 nil；
4. 容量 64 再填 100 个，每 37 次 GC，最终 64 个元素顺序正确；
5. reset 后槽位清空，可重新添加/弹出；
6. 容量 1 的环每次添加覆盖前一个，GC 后只剩最新节点。

# 期望行为

1. 前 16 次添加无覆盖；之后每次覆盖的 id 恰好是 added-16；
2. 任何一次 GC 后，环内元素按 head 顺序 id 连续、name/pad 正确；
3. grow(64) 后 len(buf)==64、count==16，元素顺序与扩容前一致；
4. popFront 返回 1984..1993，之后剩余 6 个为 1994..1999，弹空后返回 nil；
5. 第二次 churn 后保留 5036..5099 共 64 个，GC 前后一致；
6. reset 后 count==0，重新加入 9000..9004 后顺序正确；
7. 容量 1 的环添加 9100..9104，始终只保留最后一个且 GC 后可读；
8. 最后一行 `ALL PASS l13_ptr_ring_buffer`。

# 失败判读

- 覆盖顺序错：满窗时旧值返回或 head 前移错误；
- 环元素 id 不连续：取模下标或扩容拷贝顺序错误；
- GC 后节点字段损坏/崩溃：`[]*node` 未被正确扫描，旧元素被回收（UAF）；
- popFront 后 count/head 不一致：弹出时未清零槽位或计数错误；
- 容量 1 路径失败：回绕边界（head 与 count 同为 0/1）处理错误。

# 参数

- `UAF_SEED`：节点 pad 内容的种子（失败输出带该种子），不改变操作序列。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_ptr_ring_buffer/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
