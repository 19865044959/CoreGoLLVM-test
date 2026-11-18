---
id: l05_graph_cycle_churn
dim: L05_alloc_gc
priority: P2
sources:
  - sonic@v1.13.2: encode_test.go:255 的 TestSamePointerNoCycle：共享指针不构成环
  - sonic@v1.13.2: issue_test/issue600_test.go:28 的 TestIssue600：节点链表重接/删除后的可达性
tags: [L05, object-graph, cycles, shared-subgraph, generational-churn, liveness, gc, torture]
requires: []
runtime: "<10s（默认 2 轮）"
---

# 场景

每轮构建 48 张对象图，每张 24 个节点，含 8 节点环、树形子图、回边以及所有图
共享的 hub（hub 带自环）。每轮 pin 3 张图，其余整批丢弃；构建后、丢弃后各做
垃圾分配 + 两次 `runtime.GC()`，再对 pin 图做环安全遍历校验。随后改写 pin 节点
的边并追加自环，GC 后再次校验；每轮重建新图与旧 pin 图共存。默认 2 轮，
`TORTURE_ROUNDS` 可放大到 8 轮。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_graph_cycle_churn`；
2. 每张图从根可达节点数恰为 25（24 个自有节点 + 共享 hub）；
3. 构建后 GC、丢弃非 pin 图并 GC 后，pin 图摘要不变且遍历在有环时仍终止；
4. 改写 pin 节点的边和 tag 后，重新计算的摘要跨 GC 保持不变；
5. 每轮新图的节点 id、payload 与种子推导值一致，且 hub 只出现一次；
6. 每轮结束 pin 总数为 `3×轮数`，最终全部可校验；
7. 释放所有图后 hub 自环仍有效，环图整体可被回收而不影响校验结果。

## 失败判读

- `reachable=... want=25`：构建期丢失边或节点，环/共享 hub 未建立；
- `digest changed` / `count`：GC 后节点字段或边被提前回收/复用（UAF），或写屏障漏标；
- `carried graph corrupted`：仅由局部变量持有的图未被保活；
- `fresh graph ...`：重建路径在 GC 中复用旧图内存导致内容破坏；
- 超时：环遍历缺失 visited 导致死循环，或 `TORTURE_ROUNDS` 过大。

## 参数

- `UAF_SEED`：决定 payload 字节模式（默认 1，解析失败回退 1），失败汇总行带出种子；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–8；放大后每轮新建 48 张图。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_graph_cycle_churn/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=6 /tmp/case
```
