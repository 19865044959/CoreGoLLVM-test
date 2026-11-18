---
id: l11_defer_panic_gc
dim: L11_compiler_opts
priority: P1
sources:
  - gorm@v1.30.0: tests/transaction_test.go:97 TestTransactionWithBlock 的 assertPanic 中 defer recover
  - goccy-json@v0.10.5: encode_test.go:483 TestDebugMode defer + recover 包裹 Marshaler panic
  - klauspost-compress@v1.17.11: gzhttp/compress_test.go:353 TestMustNewGzipLevelHandlerWillPanic defer recover 校验 panic
tags: [defer, panic-unwind, recover, named-result, typed-nil, defer-order, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟事务回滚、调试模式与压缩库构造器中的 defer/panic 路径：panic 展开过程中多层 deferred 闭包触发显式 GC 并读取捕获的堆对象；recover 中再次 GC 后读取 named result、panic 指针与结构体 panic 值；内层 recover 后重新 panic，外层 defer 顺序与 GC 必须稳定；typed nil 由 deferred 闭包捕获。

# 期望行为

1. panic 展开时 deferred 闭包的执行顺序为后进先出，轨迹为 2、1、99；
2. recover 中 GC 后 panic 指针类型断言成功，`msg == "boom"`，named result 等于 `7+3*(n-1)`；
3. 重复 panic 路径的轨迹为 2、1、90，最终 recover 到的消息为 `"second"`；
4. deferred 闭包捕获的 typed nil 接口在 GC 后仍非 nil，断言得到 nil 指针；
5. 结构体 panic 值经 GC 后按值断言成功，字段正确；
6. 三个 deferred 闭包（其中一个内部 GC）按 30、20、10 顺序执行，和等于 `n(n+1)/2+1+n`；
7. 单元素 deferred 链的和为 3；
8. 保留的 panic 路径对象与 panic 值在最终 GC 后完整；
9. 默认构建与 `-gcflags='all=-N -l'` 构建全部断言一致；
10. 最后一行输出 `ALL PASS l11_defer_panic_gc`。

# 失败判读

- `defer order during panic unwind` / `repanic defer order`：defer 链或 panic 展开顺序被改写；
- `panic value after GC type assertion`：panic 接口值未被正确保活；
- `recover named result after GC`：named result 被内联/寄存器分配破坏；
- `typed nil captured by deferred closure`：typed nil 在 defer 环境中被折叠；
- `deferred chain LIFO with GC`：defer 内部 GC 导致栈上捕获对象失效；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定节点数 40..80，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P1，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_defer_panic_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_defer_panic_gc/*.go
UAF_SEED=1 /tmp/case_n
```
