---
id: l02_atomic_pointer_config
dim: L02_concurrency
priority: P1
sources:
  - caddy@v2.10.0: admin_test.go:147 100 goroutine 并发 Load+provision 配置，读多写少的全局配置切换
  - frp@v0.67.0: pkg/config/load_test.go:59 同一配置被 N goroutine 反复解码并校验
tags: [L02, atomic.Value, hot-reload, immutable-config, pointer, generation, gc]
requires: []
runtime: "<2s"
---

# 场景

模拟 caddy 管理端配置热切换与 frp 并发配置校验：单写者用 `atomic.Value` 连续 Store 160 个不可变 `*config`（每代含 4 个 `*node` 与固定 blob），6 个读者无锁 `Load` 并校验对象内部一致性（`limit`、节点、blob 与 `gen` 的固定关系），同时断言同一读者观察到的代数不回退。读者每 25 次读取显式 GC，并把已 Load 的旧配置指针跨 GC 再校验；写者每 17 次 Store 前也把新配置跨 GC 校验一次。

# 期望行为

1. 任意一次 `Load` 得到的配置都满足 `limit == gen%97+1`、4 个节点与 blob 按 `gen` 派生的固定值；
2. 同一读者连续 Load 的 `gen` 单调不减（atomic.Value 的顺序一致性）；
3. 旧配置在被后续 Store 替换后，只要仍被读者持有，跨 GC 内容不变；
4. 全部 goroutine 结束后最终配置 `gen == 160` 且通过完整校验；
5. 最后一行 `ALL PASS l02_atomic_pointer_config`。

# 失败判读

- `reader loaded inconsistent config`：读到半初始化/被复用的配置对象，热切换发布顺序有误；
- `config generation regressed`：atomic.Value 读写顺序性被破坏，或写者复用并回退了 gen；
- `config snapshot corrupted after GC` / `new config corrupted by GC before store`：配置对象指针在 GC 后失效；
- `final generation`：最后一次 Store 丢失。

# 参数

无随机路径；`UAF_SEED` 由 runner 注入但本用例不使用。无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_atomic_pointer_config/*.go
GODEBUG=clobberfree=1 /tmp/case
```
