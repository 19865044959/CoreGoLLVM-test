---
id: l12_var_init_call_memo
dim: L12_init_link
priority: P2
sources:
  - go-redis@v9.8.0: example_test.go:18 init() 通过函数构造全局 client 并赋给包级变量
  - go-redis@v9.8.0: main_test.go:56 包级集群对象在启动期构造 map/slice
  - fiber@v2.52.6: router_test.go:25 init() 解析 fixture 填充包级路由变量
tags: [L12, package-init, var-initializer, function-call, memoization, pointer-identity, closures, gc, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 20000 次 post-init 查找；TORTURE_ROUNDS 放大到 320000 次）"
---

# 场景

多个包级变量初始化器调用同一个记忆化构造函数 `buildCfg(key, base)`：`cfgA` 与 `cfgA2` 请求同一个 key，`cfgB` 请求另一个 key；`alias`、闭包 `adder`、方法值 `boundView` 也由变量初始化器产生。显式引用（`base` 作为参数、`cfgA`/`cfgB` 作为实参）建立规范保证的初始化依赖；init 在全部变量初始化后捕获调用次数与分配次数。主程序验证共享指针、闭包/方法值，并用 `TORTURE_ROUNDS` 放大重复查找与显式 GC，确认命中 memo 不再分配。

# 期望行为

1. `base.seed==11`、`cfgA.value==511`、`cfgB.value==411`；
2. `cfgA2==cfgA`、`alias==cfgA`、`cfgA!=cfgB`，两个 key 的 `base` 都指向同一个 `base`；
3. `built` 表中 `alpha/beta` 指向对应对象，`allocCounts` 每 key 为 1，`buildCalls==3`；
4. init 捕获 `initCalls==3`、`initAllocs==2`，证明变量初始化先于 init；
5. `adder(5)==416`、`view=="alpha=511"`、`boundView()=="alpha=511"`；
6. post-init 重复查找只增加调用计数，不增加分配，对象指针不变；
7. 并发命中 + 显式 GC 后指针、闭包与计数保持一致；
8. 最后一行 `ALL PASS l12_var_init_call_memo`。

# 失败判读

- `cfgA == cfgA2` 失败：memo 未命中或两个变量拿到不同对象；
- `allocCounts` >1：重复构造，初始化器之间缺少共享状态；
- `init observes final constructor counters` 失败：init 早于变量初始化；
- post-init 查找仍分配：memo 查表逻辑被错误重建；
- GC 后闭包/方法值错误：函数值捕获的指针未被扫描。

# 参数

- `TORTURE_ROUNDS=1..16`：post-init 重复查找次数倍数，默认 1（20000 次）；
- `UAF_SEED`：循环起始偏移，失败信息带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_var_init_call_memo/*.go
TORTURE_ROUNDS=8 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
