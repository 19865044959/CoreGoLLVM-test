---
id: l17_spill_iface_matrix
dim: L17_stackmap
priority: P2
sources:
  - grpc-go@v1.71.1: test/channelz_test.go:766（UnaryCall wrapper 的 opts ...grpc.CallOption 转发）
  - gorm@v1.30.0: tests/callbacks_test.go:123（callMethod(s interface{}, name string, args ...interface{}) 接口+变参混合调用）
tags: [L17, stackmap, spill, interface-matrix, register-abi, morestack, non-empty-interface, TORTURE_ROUNDS, GC, UAF_SEED]
requires: []
runtime: "<10s（默认 8 轮；TORTURE_ROUNDS 放大轮数，上限 1024）"
---

# 场景

`matrix` 同时接收 16 个 `any`（指针、pair、[]*item、string、nil、typed nil、map、[2]*item、闭包、chan、含非空接口字段的 struct、*[]*item、嵌套 []any、typed nil *scorer、int64）、3 个 pair、`[4]*item` 与 `[4]any`，参数总量超过寄存器后落入溢出槽。函数内先全量扫描，再经 24 层 192B 栈帧递归触发 morestack/栈复制，GC 后重扫；随后把同样参数传给 noinline `observe` 再 GC 重扫。caller 在调用前后各做一次全量扫描。

`UAF_SEED` 决定每轮 base；`TORTURE_ROUNDS` 放大轮数。

# 期望行为

1. matrix 内 GC/深递归前后两轮扫描 sum 完全一致；
2. observe 的 22 个参数在两轮 GC 后 sum 与 matrix 记录一致；
3. caller 在调用返回 + GC 后重扫全部值，sum 不变；
4. 闭包捕获 item、chan 缓冲中的 *item、非空接口字段、map 值在 GC 后均可解引用；
5. 24 层递归不破坏任何溢出槽中的 iface pair；
6. 退出码 0，最后一行 `ALL PASS l17_spill_iface_matrix`。

# 失败判读

- `args changed across GC/deep`：寄存器溢出槽或栈帧指针位图漏标；
- `observe sum`：跨调用参数扫描错误；
- `caller matrix changed`：caller 帧接口值被破坏；
- `nil item` / `item chain corrupted`：iface data word 失效；
- `empty channel`：channel 缓冲中的指针被回收或扫描错误。

# 参数

- `UAF_SEED`：每轮 base 种子，失败输出带该值；
- `TORTURE_ROUNDS=N`：默认 8 轮，上限 1024。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_spill_iface_matrix/*.go
UAF_SEED=1 TORTURE_ROUNDS=32 GODEBUG=clobberfree=1 /tmp/case
```
