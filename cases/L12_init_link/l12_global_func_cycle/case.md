---
id: l12_global_func_cycle
dim: L12_init_link
priority: P2
sources:
  - hugo@v0.155.0: common/predicate/predicate_test.go:99 var intP1 = func(i int) predicate.Match{...} 包级函数值
  - gitea@v1.23.5: modules/setting/service_test.go:56 kases := map[string]func(){...} 函数值派发表
  - tailscale@v1.80.2: util/deephash/deephash_test.go:1092 hashers := map[string]func(*T) Sum{...} 全局函数值表
tags: [L12, module, global-variables, func-values, mutual-recursion, late-binding, method-value, interface-func, gc, TORTURE_ROUNDS]
requires: [pkgbuild]
runtime: "<10s（默认 8 worker × 400 迭代；TORTURE_ROUNDS 1..8）"
---

# 场景

`hooks` 包在 init 中把包级函数变量接成图：`even`/`odd` 通过包变量互相递归、`fib` 自引用、方法值绑定到全局指针 receiver、`chain`/`table`/interface 持有函数值。main 的包级变量初始化器直接调用这些函数值（`fib(10)=55`、`lookup("hop")(5)=15`、interface 函数值、chain），证明 hooks.init 先于 main 变量初始化；随后并发派发并显式 GC。

## 期望行为

1. `probeFib==55`、`probeHop==15`、`probeIface==42`、`viaChain==42`，main init 中 `initProbes==true`；nil 全局函数值可比较且晚绑定后可调用；
2. even/odd 在深度 0/10/1001/2048 全部互补正确；`fib(0..10)` 与查表一致，`fib(20)==6765`；
3. chain 长度 3，三个函数结果 5/8/55；table 长度 5，inc/double/fib/hop 派发正确，缺失 key 返回 nil；interface 中函数值可断言调用；
4. 方法值绑定指针 receiver：改 base 后已绑定方法值看到新值（9→10、11→15）；
5. `TORTURE_ROUNDS` 放大并发 worker/迭代，按 `n%64` 派发 even/odd/fib/table/method/chain 全部正确；
6. 显式 GC 期间并发只读派发无错误；
7. GC 后函数图、递归结果、table/chain、package-level probes 与 interface 函数值不变；
8. 最后一行 `ALL PASS l12_global_func_cycle`。

## 失败判读

- main 包级初始化器看到未接好的函数图：跨包 init 顺序或依赖分析错误；
- even/odd/fib 结果错误：全局函数变量重定位、自引用/互引用 late binding 错误；
- 方法值不反映 receiver 修改：方法值复制了 receiver 而非绑定指针；
- table/interface/chain 丢失函数值：静态 funcval 或闭包捕获被 GC 破坏；
- 并发阶段失败：只读 funcval 被改写或可见性错误；
- 失败行 `seed=` 可用于复现派发起点。

## 参数

- `TORTURE_ROUNDS`（默认 1，上限 8）：worker 数 = 8×rounds（上限 64），迭代 = 400×rounds（上限 3200）；
- `UAF_SEED`：并发起点与 GC 轮数。

## 运行

```bash
cd cases/L12_init_link/l12_global_func_cycle
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=8 UAF_SEED=7 /tmp/case
```
