---
id: l18_pool_finalizer_combo
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - nats-server@v2.11.0: server/ipqueue_test.go:291（sync.Pool 切片复用与 GC 淘汰）
  - nats-server@v2.11.0: server/norace_test.go:10072（对象 finalizer + GC 轮询验证引用链断开）
tags: [L18, sync.Pool, finalizer, liveness, gc]
requires: []
runtime: "<10s"
---

# 场景

所有池化对象在创建时设置 finalizer，并用原子标记记录是否被终结：

1. 从全局 `sync.Pool` 取 256 个对象并由全局切片长期持有；它们必须始终不被终结；
2. 在 noinline 函数内构造局部 `sync.Pool`，每批 Put 256 个对象后返回；池与对象在返回后不可达，最终必须被终结；
3. 另做 1024 次 Get/Put churn 验证池化复用路径不影响存活判定。

失败只断言两类 finalizer 属性，不读对象 payload，避免时序假失败。

# 期望行为

1. 局部池失活后的 3×256 个对象最终全部被终结（10s 轮询上限）；
2. 全局持有的 256 个池化对象在多次 GC 与 churn 后仍未被终结；
3. 失败信息仅给出未终结/被提前终结的对象数；
4. 退出码 0，最后一行 `ALL PASS l18_pool_finalizer_combo`。

# 失败判读

- `N pooled objects not finalized after pool became unreachable`：池/对象仍有隐藏引用，或 finalizer 未被调度；
- `N externally-held pooled objects were finalized`：外部可达对象被误判死亡，检查池化对象栈/堆根扫描；
- 两类问题都应先在官方 Go 1.24.1 上复跑，区分编译器/运行时 bug 与环境差异。

# 参数

无随机路径；固定 256 持有、3×256 失活、1024 次 churn。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_pool_finalizer_combo/main.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
