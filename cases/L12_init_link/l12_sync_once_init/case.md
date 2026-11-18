---
id: l12_sync_once_init
dim: L12_init_link
priority: P2
sources:
  - caddy@v2.10.0: modules/caddyhttp/metrics_test.go:30 sync.Once 初始化 + in-flight 归零
  - kitex@v0.13.0: pkg/remote/trans/nphttp2/mocks_test.go:706 生成代码 init() + sync.Once 懒初始化
tags: [L12, sync.Once, lazy-init, concurrency, gc, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 256 项 × 16 goroutine；TORTURE_ROUNDS 放大表项与迭代数）"
---

# 场景

全局查找表通过 `sync.Once` 懒构建，构建内容包括 map、key 切片、带 buffer 的节点链。16 个 goroutine 在首次调用时并发竞争 `ensureTable`，主 goroutine 同时制造垃圾并触发 GC；随后验证表指针唯一、构建计数为 1、内容完整。P2 用例，`TORTURE_ROUNDS` 放大表规模与迭代次数。

# 期望行为

1. 无论多少 goroutine 并发首次调用，`buildTable` 恰好执行一次（计数=1）；
2. 所有 goroutine 观察到同一个表指针；
3. 表内每项 key/value/buffer 内容与构建规则一致，节点链长度等于表项数；
4. 并发阶段主 goroutine 的 GC 不导致表重建或内容损坏；
5. GC 后再次调用 `ensureTable` 返回同一指针，计数仍为 1；
6. 最后一行 `ALL PASS l12_sync_once_init`。

# 失败判读

- 构建计数 >1：`sync.Once` 语义或抢占/内存序错误；
- 不同指针：懒初始化存在竞态，或全局指针写入未对其它 P 可见；
- 表项缺失/值错：构建过程被 GC 打断后状态不一致；
- 死锁/超时：Once 内重入或锁顺序问题。

# 参数

- `TORTURE_ROUNDS`（默认 1，上限 16）：表项数 = `256 × rounds`（上限 65536），迭代数同步放大；
- `UAF_SEED`：决定每个 goroutine 的起始 key 偏移；断言与遍历顺序无关，失败输出带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_sync_once_init/*.go
UAF_SEED=1 GOGC=1 /tmp/case
TORTURE_ROUNDS=8 UAF_SEED=3 /tmp/case
```
