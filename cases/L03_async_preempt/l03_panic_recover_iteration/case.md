---
id: l03_panic_recover_iteration
dim: L03_async_preempt
priority: P1
sources:
  - gorm@v1.30.0: tests/transaction_test.go:156 事务回调内 panic("force panic")，外层 defer/recover 后必须完成回滚
  - etcd@v3.5.0: pkg/pbutil/pbutil_test.go:31 TestMarshalerPanic：Must 封装的 Marshal 失败即 panic，由 defer/recover 断言
tags: [L03, async-preempt, panic-recover, every-iteration, payload-interface, stack-unwind, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

循环内逐轮 panic/recover：每轮调用 noinline 的 `pnPanicIter`，按轮次对 3 取模 panic 三种载荷之一——新分配的堆节点指针、确定性字符串、含指针字段的结构体值；函数内 defer 立即 recover 并把载荷作为 `any` 返回。主循环每 64 轮在 recover 之后、校验之前分配垃圾并显式 `runtime.GC()`，使 recovered 接口载荷成为该节点/字符串的唯一根并跨 GC 存活，然后做类型断言与字段校验。另有 8 轮并发 GC pump。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l03_panic_recover_iteration`；
2. 4096 轮全部 recover 成功，无 nil 载荷；
3. 三种载荷的计数与各自校验和等于按轮次公式计算的期望值；
4. GC 后指针载荷/结构体指针字段仍可解引用，字符串内容不变；
5. 主循环显式 GC 次数为 64，GC pump 至少完成 1 轮；
6. 在 clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `FAIL: panic payload kind counts`：recover 次数/类型分派错误，或 panic 路径被跳过；
- `FAIL: recover payload valid after GC`：recover 出的接口载荷指针未被扫描，GC 后读到垃圾；
- `FAIL: recovered payload checksums`：载荷值被破坏；
- `SIGSEGV` / `fatal error: found bad pointer`：疑似栈映射或 defer 记录中的指针位图问题。

## 参数

- `UAF_SEED`：失败信息带出种子；载荷序列固定，种子仅用于定位；
- 其他 TORTURE_* 参数：本用例不消费。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_panic_recover_iteration/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
