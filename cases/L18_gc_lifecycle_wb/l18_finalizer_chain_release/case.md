---
id: l18_finalizer_chain_release
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - nats-server@v2.11.0: server/norace_test.go:10072（TestNoRaceConnectionObjectReleased：连接对象 finalizer + GC 轮询）
  - etcd@v3.5.0: server/mvcc/kvstore_test.go:653（TestConcurrentReadNotBlockingWrite：快照在版本切换后仍可读）
tags: [L18, finalizer, chain, reachability, gc-poll]
requires: []
runtime: "<10s"
---

# 场景

构造两条互不相连的 32 节点单链（A: id 100–131，B: id 200–231），每个节点仅
通过 next 指向后继，均注册只记录 id 的 finalizer。全局仅持两条链的 head。

1. 两链均可达时做 20 轮 GC 轮询，任何 finalizer 都不得触发；
2. 释放 A 的 head 后轮询至 A 的 32 个 id 全部入账，期间 B 不得被终结；
3. 释放 B 的 head 后轮询至 B 全部入账；
4. 断言每个节点恰好终结一次、无非法 id。

只断言“可达不终结 / 失活最终终结”，不依赖 finalizer 顺序，也不读节点 payload。
不使用环形结构，避免 Go 运行时对“环内含 finalizer”不保证终结的语义。

## 期望行为

1. 可达阶段链 A/B 的 finalizer 标记全部为 0；
2. 释放 A 后 A 的 32 个节点在 4s 上限内全部终结，B 保持 0；
3. 释放 B 后 B 的 32 个节点全部终结；
4. 64 个节点 finalizer 各执行一次，无重复、无非法 id；
5. 退出码 0，最后一行 `ALL PASS l18_finalizer_chain_release`。

## 失败判读

- `chain ... finalized while globally reachable`：可达性判定错误；
- `chain ... not fully finalized after release`：对象被隐藏引用保活或 finalizer 未运行；
- `finalizer count != 1`：对象复活后重复终结，或标记/队列实现错误；
- 本用例不校验终结顺序，顺序差异不构成失败。

## 参数

- `UAF_SEED`：节点数据 salt 与失败信息，不影响终结判定；
- 无 TORTURE 参数；等待上限固定 4s/阶段。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_finalizer_chain_release/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
