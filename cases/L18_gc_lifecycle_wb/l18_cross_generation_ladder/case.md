---
id: l18_cross_generation_ladder
dim: L18_gc_lifecycle_wb
priority: P2
sources:
  - quic-go@v0.50.1: send_stream_test.go:985（TestSendStreamRetransmitDataUntilAcknowledged：发送窗口跨代保留帧数据）
  - badger@v4.5.0: skl/skl_test.go:132（TestConcurrentBasic：多层节点把新节点挂到不同年龄节点）
  - badger@v4.5.0: db_test.go:996（TestIterateParallel：共享快照与并发迭代器生命周期）
tags: [L18, cross-generation, old-to-young, young-to-old, map, interface, ladder, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 6 轮；TORTURE_ROUNDS 放大轮数，上限 30）"
---

# 场景

noinline `cg2Build` 依次创建 5 个 cohort（每层 6 节点），每创建一个就做两轮 GC，使 5 层获得不同年龄；随后用链、peers map 与 interface 建立跨代引用（老→新、新→老、老→老），再 GC。

每轮 noinline `cg2Grow` 沿链收集所有节点，追加一个全新年轻 cohort：

1. `cg2LinkTail` 把老 tail 的 next 指向年轻 head，年轻 head 的 prev 指回老 tail；
2. `cg2SetMap` 把老 cohort0 的 map["young"] 与老 cohort4 的 map["next"] 指向年轻节点；
3. `cg2SetIface` 把老 cohort2 的 interface 更新为年轻节点；
4. 年轻节点自身通过 map["old"]/map["prevgen"] 与 interface 反指最老 cohort 和上一 cohort；
5. GC 后沿整条阶梯校验 ID、cohort/name/data、线性 prev/next、peers 跨代边与 interface。

# 期望行为

1. 5 个初始 cohort 的节点在 GC 后全部存活，阶梯顺序与双向链接正确；
2. 每轮追加的年轻 cohort 全部跨 GC 存活，阶梯长度为 `(5+轮数)*6`；
3. 老→年轻 map/interface、年轻→老 map/interface、老→老 map 边全部保持；
4. 各族群的 `any` 目标符合代次规则（cohort0→2、cohort1→3、cohort2→最新、cohort3/4 为 nil、年轻 cohort→cohort0）；
5. 失败信息带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_cross_generation_ladder`。

# 失败判读

- `ladder length=...`：老 tail→年轻 head 的写屏障缺失，年轻 cohort 被回收或链断裂；
- `prev/next link broken`：双向链一端丢失；
- `old map->young broken`：老 map 写入年轻指针未生效（map value 写屏障）；
- `old interface->youngest broken`：老 interface 字段写屏障缺失；
- `young map->old broken`：年轻对象反指老对象时提前死亡。

# 参数

- `UAF_SEED`：数据 salt（seed%13）与失败信息；
- `TORTURE_ROUNDS`：追加轮数（默认 6，上限 30，即最多 35 个 cohort / 210 节点）。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_cross_generation_ladder/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=20 UAF_SEED=6 /tmp/case
```
