---
id: l18_old_chain_bridge_graft
dim: L18_gc_lifecycle_wb
priority: P0
sources:
  - badger@v4.5.0: skl/skl_test.go:132（TestConcurrentBasic：skiplist 多层节点把新节点挂到不同年龄节点上）
  - quic-go@v0.50.1: send_stream_test.go:985（TestSendStreamRetransmitDataUntilAcknowledged：发送窗口跨重传保留帧链）
tags: [L18, write-barrier, old-to-young, doubly-linked-list, noinline, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

先构造两条各 16 节点的双向链（id 0..15 与 100..115），经两轮 GC 整体晋升为老对象。随后 8 轮（`TORTURE_ROUNDS` 可放大到 32）：

1. 按 `UAF_SEED` 与轮次奇偶，选择先由 noinline `obDetach` 断开 `aTail.next` 与 `bHead.prev`（老→nil），GC 后校验两条链各自完整；
2. noinline `obMakeBridge` 创建年轻 bridge 节点，`obSetBridge` 把 `aTail→bridge→bHead` 同时写回双向指针；
3. GC 后从 head 正向、从 tail 反向全量遍历，校验 33 个节点的 id/name/data、双向一致性与 bridge 端点。

直接替换路径下，上一轮 bridge 被旁路后失活，不参与断言。

# 期望行为

1. 断开后的两条老链各自长度、顺序、prev/next 与数据完整；
2. 每轮拼接后链长为 `32+1`，bridge id 为 `5000+轮次`，aTail/bHead 与 bridge 四向指针一致；
3. 48 个老节点的数据在全部轮次中不漂移，年轻 bridge 跨 GC 存活；
4. 失败输出带 `seed=<UAF_SEED>`；
5. 退出码 0，最后一行 `ALL PASS l18_old_chain_bridge_graft`。

# 失败判读

- `chain ended ...`：老→年轻指针或老→nil 写入丢失，链被截断；
- `forward prev link broken`：年轻 bridge 的 prev 未写回老 bHead（或反向写屏障缺失）；
- `node ... data corrupted`：对象被提前回收或内存复用（clobberfree/efence 下更明显）；
- 超时：不涉及并发，通常是运行时卡死或 OOM。

# 参数

- `UAF_SEED`：控制是否走“先断开再拼接”路径以及起始 id 偏移，失败信息带出；
- `TORTURE_ROUNDS`：轮数（默认 8，上限 32）。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_old_chain_bridge_graft/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=24 UAF_SEED=5 /tmp/case
```
