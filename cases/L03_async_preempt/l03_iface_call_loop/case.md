---
id: l03_iface_call_loop
dim: L03_async_preempt
priority: P1
sources:
  - grpc-go@v1.71.1: benchmark/primitives/primitives_test.go:365 热循环中对 any 做接口类型断言，验证 itab 命中路径
  - grpc-go@v1.71.1: encoding/encoding_test.go:70 compressor 注册表，通过接口 Name()/Compress()/Decompress() 动态分发
tags: [L03, async-preempt, interface-dispatch, itab, pointer-holder, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

构造 256 个 `summer` 接口值，由 `implA`/`implB` 两种实现交替组成，二者都持有 `*iNode`。主 goroutine 在 524288 次以上迭代中按 `UAF_SEED` 派生的步长选取接口值，调用 `add()`（每 128 轮再调用 `id()`），每次调用都经过 itab 动态分发；每 64K 轮分配垃圾并显式 `runtime.GC()`，GC pump 并发执行 8 轮 GC。

# 期望行为

1. 并发 GC pump 至少完成一轮；
2. 循环至少执行 524288 次；
3. 累加和等于按两种实现公式（含 `id()` 的周期项）推算的参考值；
4. GC 后逐个类型断言，所有接口持有的节点字段不变；
5. 进程退出码为 0，最后一行 `ALL PASS l03_iface_call_loop`；
6. 各 profile 下结果一致。

# 失败判读

- `FAIL: GC pump completed during iface loop`：长循环期间 GC 未完成；
- `FAIL: interface dispatch checksum (seed=...)`：动态分发结果错误或接口持有的指针被破坏；
- `FAIL: iface-held nodes intact after GC (seed=...)`：接口值中的指针未保活或 itab 被破坏；
- 超时：循环与 GC 互锁。

# 参数

- `UAF_SEED`：接口选取步长、偏移与节点映射偏移，默认 1，失败输出带出；
- 迭代下限 524288、显式 GC 周期 65536 均为固定常量。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_iface_call_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=4 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
