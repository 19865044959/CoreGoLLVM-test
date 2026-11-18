---
id: l11_open_coded_defer
dim: L11_compiler_opts
priority: P0
sources:
  - goccy-json@v0.10.5: encode_test.go:483 TestDebugMode defer + recover 包裹可能 panic 的 MarshalJSON
  - gorm@v1.30.0: tests/transaction_test.go:97 defer recover 辅助函数驱动事务回滚断言
  - klauspost-compress@v1.17.11: gzhttp/compress_test.go:353 TestMustNewGzipLevelHandlerWillPanic defer recover 校验 panic
tags: [open-coded-defer, defer-order, recover, named-result, stack-growth, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟序列化调试模式与 ORM 事务回滚中的 defer：两个 defer 按 LIFO 修改命名返回值；defer 参数在注册时求值而闭包捕获变量在返回时读取；8 个独立 defer 覆盖 open-coded 上限；循环内 5..12 个 defer 落回堆分配路径；嵌套函数中的 recover、双重命名结果、条件 defer、提前 return、deferred 方法调用、defer 内显式 GC 后读取被捕获对象。循环次数由 `UAF_SEED` 决定。

# 期望行为

1. LIFO 顺序使 `r=5` 经 `*2`、`+1` 得到 11；
2. defer 参数在注册时求值（`a1`），闭包捕获变量在返回时读取（`b9`），输出 `b9a1`；
3. 8 个独立 defer 按 `76543210` 执行；
4. defer 中的 recover 得到 `recovered:boom`，嵌套 recover 得到 `inner:x`；
5. 多个命名结果分别被 defer 修改为 11/22；
6. 条件 defer 结果为 106/6，提前 return 与正常 return 分别为 20/40；
7. deferred 方法在返回值确定后执行，返回 15；
8. defer 内 GC 后被捕获的 `dcNode` 字段仍为 77/kept；
9. 循环 defer 全部执行且顺序为 `n-1..0`；
10. 最后一行输出 `ALL PASS l11_open_coded_defer`。

# 失败判读

- `LIFO order` / `loop defers LIFO order`：open-coded/heap defer 顺序错误；
- `defer argument evaluation time`：参数求值时机被优化错；
- `eight open-coded defers`：open-coded defer 表越界或顺序错乱；
- `recover in defer` / `nested recover`：recover 控制流被破坏；
- `defer closure sees object after GC`：defer 捕获对象未被保留或 GC 后字段损坏；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定循环 defer 个数 5..12，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P0，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_open_coded_defer/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_open_coded_defer/*.go
UAF_SEED=1 /tmp/case_n
```
