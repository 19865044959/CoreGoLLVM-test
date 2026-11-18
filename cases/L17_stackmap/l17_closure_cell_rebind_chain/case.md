---
id: l17_closure_cell_rebind_chain
dim: L17_stackmap
priority: P1
sources:
  - grpc-go@v1.71.1: xds/internal/balancer/clusterimpl/balancer_test.go:331（dones = append(dones, func(){...}) 闭包表共享循环状态）
  - klauspost/compress@v1.17.11: zstd/dict_test.go:220（encs 闭包切片捕获同一组变量）
tags: [L17, stackmap, closure, mutated-capture, shared-cell, interface-dispatch, GC, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

`build` 创建四个闭包，它们共享同一个捕获单元 `cur *item` 与 `seen []*item`：读闭包读 `cur.v`，突变闭包重绑 `cur = mkItem(...)` 并 append 到 `seen`，另两个闭包读取重绑后的值与 `len(seen)`。闭包表经接口 `stepper.step()` 逐次调用，期望值由 caller 的独立数值模型推导；每次调用后执行两轮 GC，再经捕获 getter 取 `cur` 与遍历 `seen` 校验。

`UAF_SEED` 决定每轮 base。

# 期望行为

1. 每次 `step` 的返回值与 caller 模型一致，证明所有闭包看到同一捕获单元的重绑；
2. 每步 GC 后 getter 返回的 `cur` 值等于模型值，item 链完整；
3. `seen` 中所有元素在 GC 后有效，sum 与模型累加值一致；
4. 循环结束后再 GC，捕获单元与 seen 仍完整；
5. 被重绑丢弃的旧 item 可被回收（clobberfree 下不出现悬垂读取）；
6. 退出码 0，最后一行 `ALL PASS l17_closure_cell_rebind_chain`。

# 失败判读

- `closure=... want=...`：捕获单元被按值复制，突变未共享；
- `shared capture p.v`：getter 读取到被 GC 清零/回收的对象；
- `seen sum`：闭包捕获的 slice 头或元素指针未被扫描；
- SIGSEGV：旧 item 被提前回收后仍被引用。

# 参数

- `UAF_SEED`：每轮 base 种子，失败输出带该值；
- 固定 4 轮 × 16 次接口调用。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_closure_cell_rebind_chain/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
