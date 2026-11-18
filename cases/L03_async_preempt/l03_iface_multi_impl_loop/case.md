---
id: l03_iface_multi_impl_loop
dim: L03_async_preempt
priority: P0
sources:
  - fiber@v2.52.6: router_test.go:306 通过接口断言确认 *App/*Group 都实现 Router，接口方法调用路径需保持可用
  - etcd@v3.5.0: pkg/pbutil/pbutil_test.go:23 接口 Marshaler/Unmarshaler 的 Must 封装，失败路径 panic（:31），recover 断言
tags: [L03, async-preempt, interface-dispatch, multi-impl, itab, pointer-liveness, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

构造 384 个接口值，覆盖 6 种实现（各自持有堆节点指针和独立系数）；主 goroutine 在长循环里逐轮调用 `apply`，周期性调用 `weight`/`kind`，每 32768 轮显式 GC 并分配垃圾；并发 GC pump 再执行 8 轮 GC。循环结束后用 type switch 逐个复核实现类型、系数和所持节点。

# 期望行为

1. 并发 GC pump 至少完成一轮；
2. 循环至少执行 131072 轮（上限 2M 防止死循环）；
3. 分派校验和与按闭式公式计算的参考值一致；
4. 主循环显式 GC 次数符合 `(done-1)/32768+1`；
5. 6 种实现的动态类型、kind/weight 和节点内容在 GC 后仍正确；
6. 退出码 0，最后一行 `ALL PASS l03_iface_multi_impl_loop`；
7. base/clobberfree/efence/k8s 等 profile 下结果一致。

# 失败判读

- `FAIL: multi-impl dispatch checksum`：itab 分发错误、实现体被错误内联或寄存器中的接口值被破坏；
- `FAIL: interface implementations and held nodes intact`：GC 后实现体或节点指针失效，大概率 stack map 漏标；
- `FAIL: iface backing nodes intact`：节点内容被覆盖，按 clobberfree 垃圾值定位；
- `SIGSEGV` / `fatal error: found bad pointer`：接口值或节点指针未被正确标记。

# 参数

- `UAF_SEED`：决定步长与起始偏移，失败信息带出种子；
- 无其他放大参数，P0 默认规模固定。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_iface_multi_impl_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
