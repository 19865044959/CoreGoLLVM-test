---
id: l18_graph_detach_reattach
dim: L18_gc_lifecycle_wb
priority: P2
sources:
  - quic-go@v0.50.1: connection_test.go:1511（TestConnectionPacketBuffering：握手完成前缓存包、完成后释放/重接）
  - nats-server@v2.11.0: server/client_test.go:1304（TestUnsubRace：订阅 tombstone 与重新注册）
  - etcd@v3.5.0: server/mvcc/kvstore_test.go:653（TestConcurrentReadNotBlockingWrite：快照在版本切换后仍可读）
tags: [L18, object-graph, nested-map, slice, interface, detach-reattach, old-to-old, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 6 轮；TORTURE_ROUNDS 放大轮数，上限 24）"
---

# 场景

两个老 root（A/B）各自持有 own 子树，另有一个含 kids/byName/any/peer 环的移动子树初始挂在 A 上。每轮子树在 A/B 之间迁移：

1. noinline `daDetach` 从源 root 的 slice 中过滤掉子树、删除 map entry、清空 interface，返回子树并由全局 carry 持有；GC 后校验源 root 只剩 own 子树、无 map/interface 残留，carry 子树数据完好；
2. noinline `daAttach` 把子树 append 回目标 root 的 kids，写入 map["moved"] 与 interface，追加一个年轻 leaf，并把子树 interface 回指目标 root 的 own 子树；清空 carry 后 GC；
3. 校验目标 root 三重入口指向同一子树、整棵子树 peer 环与全部 leaf，源 root 与新位置互不串扰。

# 期望行为

1. 摘除后源 root 的 kids/map/interface 均不再引用移动子树；
2. carry 持有的子树在跨 GC 窗口内数据、peer 环、内部 map 完整；
3. 重接后目标 root 的 slice/map/interface 指向同一子树，`kids[0]` 为 own 子树；
4. 每轮新增年轻 leaf 跨 GC 存活，旧 leaf 不丢失，leaf 数等于轮次；
5. 子树 interface 回指目标 own 子树（老→老），peer 环完整；
6. 失败信息带 `seed=<UAF_SEED>`；
7. 退出码 0，最后一行 `ALL PASS l18_graph_detach_reattach`。

# 失败判读

- `root containers disagree`：slice/map/interface 三者写入不一致，或其中一个漏写屏障；
- `root kids len=...`：摘除过滤错误或旧引用残留；
- `subtree peer cycle broken`：嵌套环中某条边在 GC 后丢失；
- `leaf ... mismatch`：老子树 append 年轻 leaf 后漏写屏障，年轻 leaf 被回收；
- `own map entry broken`：老→老 map 写入被破坏。

# 参数

- `UAF_SEED`：数据 salt（seed%11）与失败信息；
- `TORTURE_ROUNDS`：迁移轮数（默认 6，上限 24）。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_graph_detach_reattach/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=16 UAF_SEED=4 /tmp/case
```
