---
id: l02_once_registry_init
dim: L02_concurrency
priority: P1
sources:
  - grpc-go@v1.71.1: encoding/encoding_test.go:70 接口注册表、重复注册、强制 codec
  - tailscale@v1.80.2: types/lazy/sync_test.go:127 10000 goroutine 并发触发单次初始化
  - etcd@v3.5.0: pkg/types/set_test.go:27 线程安全 Set 的并发 Add/Contains/Values
tags: [L02, sync.Once, nested-once, registry, duplicate-registration, concurrent-lookup, panic-once, gc]
requires: []
runtime: "<3s"
---

# 场景

模拟 grpc-go codec 注册表与 tailscale lazy 初始化：`registry` 用 `sync.Once` 初始化 32 个基础项、别名表和嵌套 Once 构建的 lazy 默认项；64 个 goroutine 同时首次调用 `ensure()`，然后两两竞争注册 32 个扩展 key（每个 key 2 个 goroutine），恰好一半成功、一半收到重复错误。

另有 4 个读者并发查找基础项与别名，逐项校验 codec 自洽并在 GC 后重校验；注册完成后检查注册表长度、全部条目、别名指针一致性。最后单独验证 `sync.Once` 在函数 panic 后仍视为已执行（第二次 Do 不再运行）。

# 期望行为

1. 64+ goroutine 并发首次使用下 init 只运行 1 次，嵌套 lazy Once 也只运行 1 次；
2. 扩展注册恰好 32 成功、32 被重复拒绝；最终注册表 65 项（32 基础 + 32 扩展 + 1 lazy）；
3. 基础项 id/magic/blob 与 key 固定对应，别名查询返回同一指针；扩展项自洽且 id≥2000；
4. 并发查找结果在 GC 后不变，缺失 key 返回 false；
5. Once body panic 被 recover 后，第二次 Do 不执行；
6. 最后一行 `ALL PASS l02_once_registry_init`。

# 失败判读

- `initialized more than once` / `nested lazy Once`：Once 并发语义被破坏；
- `registration successes` / `duplicate rejections`：注册竞争丢赢家或重复检测失效；
- `registry size` / `final ext entry`：条目丢失或扩展注册覆盖了已有项；
- `lookup mismatch` / `corrupted after GC`：注册表指针或 codec payload 跨 GC 失效。

# 参数

- `UAF_SEED`：决定 codec/blob tag 派生值，失败输出带种子；
- 无 TORTURE 参数（P1 固定 64 注册者 + 4 读者 × 150 次查找）。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_once_registry_init/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
