---
id: l12_once_dependency_dag
dim: L12_init_link
priority: P2
sources:
  - caddy@v2.10.0: modules/caddyhttp/metrics_test.go:33 Metrics 的 init 字段为 sync.Once，延迟初始化指标
  - kitex@v0.13.0: internal/configutil/once_test.go:25 OptionOnce.OnceOrPanic 首次成功、再次 panic
  - grpc-go@v1.71.1: internal/grpcsync/event_test.go:65 Fire 幂等、Done 只关闭一次
tags: [L12, sync.Once, dependency-DAG, lazy-init, concurrency, happens-before, gc, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 32 goroutine × 32 迭代；TORTURE_ROUNDS 放大到 256 × 256）"
---

# 场景

四个包级 `sync.Once` 组成 DAG：`ensureBase` 是叶子，`ensureLeft`/`ensureRight` 在各自 Once body 中调用 `ensureBase`，`ensureRoot` 再调用两个分支。32 个 goroutine 并发首次调用 `ensureRoot`，主 goroutine 同时制造垃圾并显式触发 GC；Once body 内部也包含一次 GC。用例验证每个节点恰好构建一次、依赖节点先完成、所有 goroutine 观察到同一张指针图。

# 期望行为

1. `base/left/right/root` 的构建计数与 trace 各为 1；
2. trace 偏序：`base < left`、`base < right`、`left/right < root`（left 与 right 谁先未定义，不断言）；
3. `root.left==nodeLeft`、`root.right==nodeRight`、两个分支的 `base==nodeBase`，值分别为 11/12/13/133；
4. 所有并发 goroutine 看到同一个 root 指针且字段一致；
5. 二次调用 `ensureRoot` 返回同一指针且构建计数不增；
6. 显式 GC 后整张 DAG 的指针身份与 payload 保持；
7. 最后一行 `ALL PASS l12_once_dependency_dag`。

# 失败判读

- 构建计数 >1：Once 复用/内存模型实现错误；
- `all goroutines observe one root graph` 失败：缺少 happens-before，观察到部分构造对象；
- 偏序失败：Once body 未等待依赖完成；
- GC 后指针/payload 变化：Once 结果未被全局根正确扫描；
- 超时/挂起：Once 相互等待或重入死锁。

# 参数

- `TORTURE_ROUNDS=1..8`：并发 goroutine 与迭代数倍数，默认 1（32×32）；
- `UAF_SEED`：worker 起始偏移，失败信息带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_once_dependency_dag/*.go
TORTURE_ROUNDS=4 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
