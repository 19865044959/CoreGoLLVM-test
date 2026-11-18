---
id: l03_defer_capture_in_loop
dim: L03_async_preempt
priority: P1
sources:
  - tailscale@v1.80.2: types/lazy/deferred_test.go:164 DeferredInit 持有 []func() error，登记后统一执行并传播错误
  - fiber@v2.52.6: app_test.go:1708 handler 内 Goexit 时依赖 defer 闭包完成清理，进程不得永久挂起
  - gorm@v1.30.0: tests/transaction_test.go:97 事务块用 defer 闭包在 panic/错误路径完成回滚
tags: [L03, async-preempt, defer-in-loop, closure-capture, per-iteration, LIFO, pending-defer, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

每批调用 noinline 的 `dcBatch`：函数内 16 轮循环各登记一个 defer 闭包，闭包捕获 per-iteration 变量 `i` 和该轮的堆节点 `*dcNode`；登记完毕后再原地修改全部节点。每 8 批在 defer 记录尚未执行时分配垃圾并显式 `runtime.GC()`，函数返回时闭包按 LIFO 执行并把捕获的 `i` 与被修改后的节点值写入结果切片。主循环重复 2048 批，逐批校验执行顺序、值以及指针跨 GC 的可读性；另有 8 轮并发 GC pump。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l03_defer_capture_in_loop`；
2. 每批 16 个 defer 闭包全部执行，执行顺序为 15..0（LIFO）；
3. 每个闭包看到的是自己迭代的 `i`（per-iteration 捕获），而不是循环结束值；
4. 闭包在返回时读到节点原地修改后的值，证明捕获指针有效且跨 GC 存活；
5. 在 defer 记录未执行时显式 GC 共 256 次，GC pump 至少完成 1 轮；
6. 在 clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `FAIL: defer LIFO and per-iteration capture`：defer 执行顺序或循环变量捕获语义错误；
- `FAIL: captured node mutation visible after GC`：defer 闭包环境中的节点指针未被扫描/已被回收；
- `FAIL: deferred closures all executed`：defer 记录丢失，栈展开异常；
- `SIGSEGV` / `fatal error: found bad pointer`：疑似 defer 记录或闭包环境的指针位图问题。

## 参数

- `UAF_SEED`：决定每批节点基值；失败信息带出种子；
- 其他 TORTURE_* 参数：本用例不消费。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_defer_capture_in_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
