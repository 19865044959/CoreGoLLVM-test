---
id: l18_global_ptr_array_churn
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - go-redis@v9.8.0: internal_test.go:86（Ring SetAddrs 与 GetByKey 并发，分片槽位持续替换）
  - nats-server@v2.11.0: server/norace_test.go:8483（大规模订阅者共享指针，GC 前后内存校验）
tags: [L18, write-barrier, global-array, pointer-churn, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

全局固定数组 `[256]*node`（同时保留 `*[256]*node` 全局指针视图）先整组晋升。随后 12 轮 noinline `fillGlobal` 整组替换为年轻对象；再做 10 轮稀疏 churn：按 `UAF_SEED` 随机选 80–127 个槽位用 noinline `setGlobalSlot` 写入年轻对象，每 3 轮插入一次整组替换。每次写入后 GC，逐槽校验值与 `name`，并确认数组指针视图与直接数组访问一致。

# 期望行为

1. 每轮整组替换后 256 个槽位全部为新代次且内容正确；
2. 稀疏 churn 后已改槽位为新代次、未改槽位保持旧代次，无串槽；
3. `gArr[i]` 与 `(*gArrPtr)[i]` 始终一致；
4. 失败输出带 `seed=<UAF_SEED>`；
5. 退出码 0，最后一行 `ALL PASS l18_global_ptr_array_churn`。

# 失败判读

- `global array slot N corrupted`：全局数组槽位写年轻指针漏写屏障，年轻对象被回收；
- `array pointer view diverged`：数组根或指针视图被破坏（优先查全局根扫描）；
- clobberfree 下 name 变垃圾、efence 下 SIGSEGV：确认 UAF；
- 失败时用 `UAF_SEED=<seed>` 复现稀疏槽位选择。

# 参数

- `UAF_SEED`：稀疏 churn 的槽位排列种子（默认 1），不影响成功输出；
- 固定参数：256 槽、12 轮整组、10 轮稀疏。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_global_ptr_array_churn/main.go
UAF_SEED=7 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
