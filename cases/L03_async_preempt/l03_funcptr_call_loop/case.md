---
id: l03_funcptr_call_loop
dim: L03_async_preempt
priority: P1
sources:
  - grpc-go@v1.71.1: internal/idle/idle_test.go:346 空闲超时回调与 OnCallBegin 竞争，20 轮循环中插入 runtime.GC()
  - tailscale@v1.80.2: health/health_test.go:111 注册/注销 watcher，状态变化按序回调；注销后不再收到
tags: [L03, async-preempt, function-pointer, indirect-call, safe-point, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟回调注册表式的事件分发：构造 6 个 `//go:noinline` 函数值组成的函数表，主 goroutine 在 26 万次以上的循环里按 `UAF_SEED` 派生的两个步长选取节点与函数值，通过函数指针间接调用，调用返回后继续解引用同一节点的 `next` 指针。每 32768 轮分配垃圾并显式 `runtime.GC()`，另有一个 GC pump 并发执行 8 轮 GC。间接调用点本身是安全点，GC 可能落在调用返回与后继解引用之间。

# 期望行为

1. 进程退出码为 0；
2. 最后一行输出 `ALL PASS l03_funcptr_call_loop`；
3. 每次间接调用结果与 `fpRef` 直接调用参考实现一致（`bad == 0`）；
4. 全部迭代累加和与参考和相等，说明没有被优化掉的调用或丢迭代；
5. 至少完成一轮并发 GC，且每 32768 轮的主线程显式 GC 次数符合预期；
6. 全部节点字段与 `next` 环在 GC 后保持原值；
7. 在 base/clobberfree/efence/k8s 等 profile 下输出与校验结果一致。

## 失败判读

- `FAIL: indirect call results match reference`：函数表分派或调用结果错误，优先检查间接调用/内联优化；
- `FAIL: funcptr checksum after GC`：累加和丢失或指针目标被 GC 破坏；
- `SIGSEGV` / `fatal error: found bad pointer`：栈映射漏标或写屏障缺失，检查函数值/节点指针的存活区间；
- 超时：GC pump 或主循环死锁。

## 参数

- `UAF_SEED`：节点访问步长、函数选择步长与偏移均由该种子派生；失败信息带出种子；
- 无其他放大参数，P1 固定规模。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_funcptr_call_loop/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
