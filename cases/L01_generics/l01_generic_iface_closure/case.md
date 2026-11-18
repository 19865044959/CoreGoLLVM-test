---
id: l01_generic_iface_closure
dim: L01_generics
priority: P1
sources:
  - ent@v0.14.4: entc/integration/hooks/hooks_test.go:724 Limiter[Q interface{ Limit(int) }] 以方法集约束 Q 并派发两个实现
  - pgx@v5.7.4: rows_test.go:335 CollectRows 接收 RowTo[int32] 泛型函数值，按函数值调用完成逐行收集
  - gorm@v1.30.0: tests/preload_test.go:239 10 个 goroutine 各自 Session 隔离，闭包上下文不得互相污染
tags: [L01, generics, generic-interface, closure-state, middleware-chain, method-value, typed-nil, gc]
requires: [generics]
runtime: "<10s"
---

# 场景

组合泛型接口 `Transformer[T]`、泛型函数类型 `Mapper[T]`、中间件链 `Chain[T]` 与闭包：
闭包捕获计数器、基值与指针载荷 `*record`，链中混入 nil 步骤；经接口切片、方法值、
`compose` 和 typed-nil 路径调用。再以 4 个 goroutine 各自持有独立闭包状态，验证
并发下状态隔离。对应 ent 方法集约束、pgx 泛型函数值、gorm Session 隔离。

# 期望行为

1. int64 链按声明顺序应用步骤，nil 步骤被跳过，闭包 `seen` 与 `Calls` 计数精确；
2. `compose` 的空链/含 nil 步骤返回恒等或正确复合结果；
3. 接口切片经泛型 `runAll[T]` 派发，标签顺序与最终值正确；
4. 方法值调用与直接调用结果一致；
5. `*record` 经闭包与接口持有，GC 后指针身份、Path/Hits/ID 仍正确，路径追加不丢失；
6. typed-nil 装入泛型接口后 `!= nil`，零值接口 `== nil`；
7. 4 个并发 worker 的闭包计数器与结果互不影响、可复算；
8. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_generic_iface_closure`。

# 失败判读

- `closure captured state mismatch`：闭包捕获的变量被错误共享/复制；
- `generic interface dispatch ...`：泛型接口实例化后方法集或 itab 错误；
- `interface-held closure lost pointer payload after GC`：闭包/接口中的 `*record` 未被扫描；
- `typed nil lost through generic interface`：typed-nil 判定被实例化改写；
- `worker ... closure ... mismatch`：并发下闭包环境串扰或数据竞争；
- `FAILED ... seed=`：失败输出带 UAF_SEED。

# 参数

- `UAF_SEED`：决定 record 的 ID 增量（0..4）与并发循环次数（60..89），失败输出带种子。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_generic_iface_closure/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
