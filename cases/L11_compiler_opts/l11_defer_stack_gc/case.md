---
id: l11_defer_stack_gc
dim: L11_compiler_opts
priority: P1
sources:
  - caddy@v2.10.0: modules/caddyhttp/templates/tplcontext_test.go:245 defer bufPool.Put(buf) 模板执行后归还池
  - gorm@v1.30.0: tests/transaction_test.go:97 defer func() 驱动事务回滚断言
  - klauspost-compress@v1.17.11: gzhttp/compress_test.go:352 TestMustNewGzipLevelHandlerWillPanic defer recover 校验 panic
tags: [defer, recursion, stack-growth, panic-unwind, recover, method-value, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟模板执行池归还与事务回滚中的 defer：2000..2499 层递归每帧注册一个修改命名结果的 defer，触发栈增长后按 LIFO 展开；40 层 panic 展开路径验证每帧 defer 的执行顺序；另测 defer 参数注册时求值、循环内 7/9 个 defer 的每轮变量捕获、defer 闭包在显式 GC 后读取对象、deferred 方法值绑定指针接收者、defer 内触发 3000 层深递归，以及最内层 defer recover 后沿调用链返回。

# 期望行为

1. 递归 defer 的命名结果等于 `n(n+1)/2+n`，执行次数为 n+1，LIFO 顺序为 0..n；
2. panic 展开时先记录 40..0，再按 101..140 执行各帧 defer，recover 文本为 `bottom`；
3. defer 参数在注册时求值，返回 `1` 而非修改后的 `2`；
4. 循环 7 次与 9 次的 defer 均按 `n-1..0` 执行（覆盖 open-coded 上限边界）；
5. deferred 闭包在 GC 后读到对象的最终字段 78/kept；
6. deferred 方法值在返回值确定后把指针接收者从 10 加到 11；
7. defer 内深递归得到 3000；
8. defer 内接口断言在 GC 后得到 `iface`；
9. 最内层 defer recover 后，50 层调用链返回 -1；
10. 最终 GC 后递归结果与 panic trace 保持不变；
11. 最后一行输出 `ALL PASS l11_defer_stack_gc`。

# 失败判读

- `recursive defer named result` / `LIFO order`：defer 顺序或命名结果修改错误；
- `panic unwinding` / `multi-frame defer order`：panic 展开路径中 defer 被跳过或顺序错；
- `defer argument evaluated at registration`：参数求值时机被优化错；
- `loop defer ... boundary`：循环 defer 捕获或堆 defer 路径错误；
- `deferred closure reads object after GC` / `deferred interface assertion`：捕获对象未被保活；
- `deferred method value pointer receiver`：方法值接收者复制语义错误；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定递归深度 2000..2499，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P1，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_defer_stack_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_defer_stack_gc/*.go
UAF_SEED=1 /tmp/case_n
```
