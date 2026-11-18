---
id: l03_iface_closure_registry
dim: L03_async_preempt
priority: P1
sources:
  - caddy@v2.10.0: modules_test.go:22 modulesMu 保护的模块注册表，GetModules 在锁内读取 ModuleInfo 并逐个匹配
  - grpc-go@v1.71.1: stats/stats_test.go:828 checkServerStats 按顺序调用 []func 校验闭包，闭包持有测试上下文
  - tailscale@v1.80.2: types/lazy/deferred_test.go:164 DeferredInit 持有 []func() error，登记后延迟执行并传播错误
tags: [L03, async-preempt, interface-dispatch, closure, itab, indirect-call, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟模块/回调注册表：构造 256 个 `ifProc` 接口值，一半是持有 `*ifNode` 的 `*ifScale` 结构体实现，一半是捕获 `*ifNode` 与常量的闭包（`ifClosure` 实现 `run` 方法）。主 goroutine 在 65536 轮循环里按 `UAF_SEED` 派生的步长选取注册表项与环上节点，经 itab 动态分派调用 `run` 并累加；每 4096 轮分配垃圾并显式 `runtime.GC()`，GC 后立刻用同一接口值再调用一次。另一个 goroutine 并发执行 8 轮 GC。结束后按固定顺序遍历注册表，用 probe 节点复核每个闭包/结构体持有的捕获节点。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l03_iface_closure_registry`；
2. 每轮的接口调用结果等于由种子步长推导的期望值，且 GC 后复调用结果不变；
3. 全部轮次的累加和等于期望和；
4. 主循环显式 GC 次数为 16，GC pump 至少完成 1 轮；
5. 注册表两种实现各 128 个，结束时逐项调用仍能读回各自捕获的节点；
6. 在 clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `FAIL: interface/closure checksum`：闭包环境或 `ifScale.p` 指向的节点被破坏；
- `FAIL: interface dispatch result across GC`：接口值/闭包环境中的指针未被正确扫描，GC 后读到垃圾值；
- `SIGSEGV` / `fatal error: found bad pointer`：疑似栈映射或 GC 元数据漏标导致闭包环境被回收；
- 超时：GC 饥饿或 goroutine 卡死。

## 参数

- `UAF_SEED`：决定注册表项与节点的访问步长/偏移；失败信息带出种子；
- 其他 TORTURE_* 参数：本用例不消费。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_iface_closure_registry/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
