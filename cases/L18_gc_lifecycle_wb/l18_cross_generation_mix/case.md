---
id: l18_cross_generation_mix
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - badger@v4.5.0: skl/skl_test.go:132（TestConcurrentBasic：skiplist 多层节点把新节点挂到不同年龄的节点上）
  - quic-go@v0.50.1: send_stream_test.go:985（TestSendStreamRetransmitDataUntilAcknowledged：发送窗口跨重传保留帧数据）
tags: [L18, write-barrier, cross-generation, old-to-young, young-to-old, interface, cycle, noinline, UAF_SEED]
requires: []
runtime: "<5s"
---

# 场景

构造三代对象：

- gen0：12 个槽位节点，经 3 轮 GC 晋升为最老；
- gen1：4 个 mid 节点，attach 到 gen0 槽位后再经 2 轮 GC 成为次老；
- gen2：每轮由 noinline `attachCross` 新建 y→y2 两个年轻节点。

每轮按 `UAF_SEED` 选择一个 gen0 槽位与一个 gen1 mid，按种子打乱顺序写入六类边：

- 老→新：`old.next=y`、`old.any=y`、`mid.any=y2`；
- 新→老：`y.prev=old`、`y.any=old`、`y2.kids=[mid,mid]`；
- 新→新：`y.next=y2`、`y2.prev=y`。

GC 后遍历所有 12 个槽位与 4 个 mid，校验三代对象内容、双向链、接口反向引用与 kids 回指。

# 期望行为

1. 每轮 GC 后 gen0 槽位与 gen1 mid 的 id/gen/name/data 全部完好；
2. 被选中的槽位 next 为当轮 gen2 y，`y.prev` 指回槽位，且槽位 interface 与 next 指向同一 y；
3. y 的 interface 指回老槽位，y.next 链到 y2 且 y2.prev 指回 y；mid.any==y2 且 y2.kids 两项均指向 mid；
4. 未被选中槽位/ mid 无多余链接，历史轮次的链接不丢失、不串代；
5. 失败输出带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_cross_generation_mix`。

# 失败判读

- `old->young next/interface link lost`：老对象写年轻指针漏写屏障；
- `young->old ... lost`：反向引用或老节点被错误回收（老节点本应被槽位根持有）；
- `gen2 chain ... lost` / `kids link lost`：年轻→年轻或年轻→老 slice 写入未被扫描；
- `gen0/gen1 ... corrupted`：老代对象自身被破坏，优先查根扫描/写屏障实现；
- clobberfree 下 data 变 0xde、efence 下 SIGSEGV：确认 UAF；
- 失败时用 `UAF_SEED=<seed>` 复现选中槽位/ mid 与 setter 顺序。

# 参数

- `UAF_SEED`：每轮选择槽位/mid 及六类 setter 顺序的种子（默认 1），不影响成功输出；
- 固定参数：gen0 12 槽、gen1 4 mid、20 轮、每轮 2 个 gen2 节点。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_cross_generation_mix/main.go
UAF_SEED=7 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
