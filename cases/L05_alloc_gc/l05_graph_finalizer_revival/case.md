---
id: l05_graph_finalizer_revival
dim: L05_alloc_gc
priority: P2
sources:
  - go@1.24.1: src/runtime/mfinal_test.go:24 的 TestFinalizerType：finalizer 参数、接口与大对象路径
  - go@1.24.1: src/runtime/mfinal_test.go:81 的 TestFinalizerInterfaceBig：finalizer 在 GC 后异步执行并校验对象内容
tags: [L05, finalizer, object-graph, resurrection, liveness, gc, torture]
requires: []
runtime: "<10s（默认 2 轮）"
---

# 场景

排查对象图与 finalizer 的交互：每轮构建 13 张 18 节点 DAG（只保留向前边，
避免带 finalizer 的环不保证被回收）。其中 2 张 pin 住并安装 finalizer，10 张
整体丢弃后由 finalizer 异步上报根 id，另 1 张“凤凰”图在 finalizer 中把根
指针交回主协程实现复活。每轮触发多轮 GC 并收齐上报，校验收到的根恰是被丢弃
的根且每个只上报一次。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_graph_finalizer_revival`；
2. 每张图从根可达节点数恰为 18，复活图摘要与构建时一致；
3. pin 住的图在 GC 后内容不变，其 finalizer 不得上报；
4. 每个被丢弃的根 finalizer 恰好执行一次，上报集合与丢弃集合完全一致；
5. 凤凰图 finalizer 运行后根指针仍可安全遍历，后续 GC 不破坏其内容；
6. 默认 2 轮全部通过；`TORTURE_ROUNDS` 放大后 finalizer 数量线性增长且审计仍成立；
7. 在 base/clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `finalizer ... not a dropped root`：可达对象被错误终结，或 pin 图引用丢失；
- `pinned id=... was finalized while reachable`：pin 图未被正确保活（UAF/漏标）；
- `finalizer id=... ran N times`：finalizer 重复执行；
- `finalizer timeout`：finalizer 未被调度，可能 GC 未推进或对象仍可达；
- `phoenix ... digest changed`：复活对象内容被回收/复用；
- 若用例把环图作为 finalizer 根，按 Go 语义不保证回收，本用例已改为 DAG。

## 参数

- `UAF_SEED`：决定每个节点的 payload 字节与摘要（默认 1，解析失败回退 1），失败输出带种子；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–8；每轮新增 10 个丢弃根 + 1 个凤凰图。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_graph_finalizer_revival/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=8 /tmp/case
```
