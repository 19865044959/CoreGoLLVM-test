---
id: l12_global_func_values
dim: L12_init_link
priority: P2
sources:
  - gitea@v1.23.5: modules/setting/service_test.go:56 kases := map[string]func(){...} 函数值派发表
  - tailscale@v1.80.2: util/deephash/deephash_test.go:1092 hashers := map[string]func(*T) Sum{...} 函数值表
  - hugo: main_test.go:69 testscript.Main(m, map[string]func(){...}) 全局函数值命令表
tags: [L12, global-variables, func-values, method-value, method-expression, closures, dispatch, gc, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 20000 次派发；TORTURE_ROUNDS 放大到 320000 次）"
---

# 场景

包级变量初始化阶段构造函数值图：`table` 同时持有指针接收者方法值（`shared.Add`/`shared.Mul`）、捕获全局指针的闭包、普通函数值；`exprs` 持有方法表达式 `(*counter).Add/Mul`；`chain` 持有组合闭包；`bumpFn` 是另一种签名的方法值。后续包级变量初始化器直接调用 `table["add"]` 等函数值，init 仅计数。主程序验证方法值绑定指针接收者的语义、闭包捕获状态、方法表达式与组合闭包，并以单线程 + 并发派发配合显式 GC 验证函数值重定位与闭包指针扫描。

## 期望行为

1. `sumProbe==33`、`methodProbe==8`、`initExprsProbe==11`：包级变量初始化器可调用已存储的函数值；
2. `%T` of `table["add"]` 为 `main.handler`；table/exprs/chain 长度为 5/2/1，所有函数值非 nil；
3. 初始 base=7 时 add/mul/add10/double 结果正确；方法表达式对独立 receiver 结果正确；`plus3thenDouble(4)==14`；
4. stateful 闭包两次调用返回 5、8，`state.last==3`；`bumpFn` 两次返回 1、2；
5. 修改 `shared.base=9` 后，已绑定的方法值看到新 base（add/mul 结果变化），说明绑定的是指针接收者；
6. `TORTURE_ROUNDS`（默认 1，上限 16）放大单线程派发到 20000×rounds（上限 320000），累计和与独立公式一致；
7. 8 goroutine 并发只读派发 + 主 goroutine 显式 GC 无错误；
8. GC 后 table/exprs/chain 仍可调用，state 值、base 与 bump 状态保持，`bumpFn()==3`；
9. 最后一行 `ALL PASS l12_global_func_values`。

## 失败判读

- 包级初始化器调用函数值失败：函数值表未在依赖序上先于调用它的变量初始化；
- 方法值未反映 receiver 修改：方法值错误地复制了 receiver 而非绑定指针；
- 派发结果错误：函数值/闭包被重定位或改写；
- GC 后闭包状态或指针失效：捕获的指针未被扫描；
- 并发阶段失败：只读函数值表中的 funcval 数据损坏；
- 失败行 `seed=` 可用于复现派发起点。

## 参数

- `TORTURE_ROUNDS`（默认 1，上限 16）：派发次数倍数，默认 20000 次；
- `UAF_SEED`：派发/并发起点与 GC 扰动。

## 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_global_func_values/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=8 UAF_SEED=5 /tmp/case
```
