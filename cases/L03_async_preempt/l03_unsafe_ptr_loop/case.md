---
id: l03_unsafe_ptr_loop
dim: L03_async_preempt
priority: P1
sources:
  - etcd@v3.5.0: client/pkg/types/set_test.go:27 unsafe Set 与线程安全 Set 共用同一套断言
  - grpc-go@v1.71.1: mem/buffers_test.go:67 引用计数 buffer 最后一次 Free 才归还池，GC 后确认无悬挂引用
tags: [L03, async-preempt, unsafe-pointer, unsafe-add, pointer-liveness, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

在堆上分配 `[]uRec`（4096 条记录），取首元素地址作为 `unsafe.Pointer` 基址，长循环用 `unsafe.Add(base, idx*stride)` 计算记录地址并直接读取 `a`/`b` 字段。每 4096 轮分配垃圾并显式 `runtime.GC()`，GC pump 并发执行 8 轮 GC；循环结束后 `runtime.KeepAlive` 保证记录切片存活到校验完成。访问下标由 `UAF_SEED` 派生。

# 期望行为

1. 并发 GC pump 至少完成一轮；
2. 循环至少执行 262144 次；
3. 累加和等于按记录字段公式推算的参考值；
4. GC 后全部记录的 `a`/`b` 保持不变；
5. 进程退出码为 0，最后一行 `ALL PASS l03_unsafe_ptr_loop`；
6. 各 profile 下结果一致。

# 失败判读

- `FAIL: GC pump completed during unsafe loop`：unsafe 读循环期间 GC 未完成；
- `FAIL: unsafe pointer read checksum (seed=...)`：通过 unsafe 指针读到的数据错误，检查基址是否被错误回收；
- `FAIL: unsafe-read records intact after GC (seed=...)`：底层切片被 GC 回收或字段被改写；
- SIGSEGV：基址失效或偏移越界。

# 参数

- `UAF_SEED`：读取步长与偏移，默认 1，失败输出带出；
- 迭代下限 262144、显式 GC 周期 4096 均为固定常量。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_unsafe_ptr_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=6 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1,asyncpreemptoff=0 /tmp/case
```
