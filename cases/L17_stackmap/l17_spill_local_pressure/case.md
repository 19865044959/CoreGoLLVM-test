---
id: l17_spill_local_pressure
dim: L17_stackmap
priority: P1
sources:
  - quic-go@v0.50.1: send_stream_test.go:985（4MB 发送窗口/ACK 状态多指针跨调用点存活）
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1493（大 struct 反射视图多字段并存）
tags: [L17, stackmap, spill, register-abi, mixed-args, local-pressure, pointer-array, GC, UAF_SEED]
requires: []
runtime: "<10s（默认 6 轮）"
---

# 场景

两条 spill 路径：

1. `mixABI` 接收 8 个 `float64`、10 个 `*item`、4 个 string 与 `*accum`，混合 ABI 下大量参数只能放 callee 栈溢出槽；callee 内再把它们复制进 `[10]*item`、`[4]string`、`[8]float64` 数组，跨 noinline `probe`（内部 GC）与两次显式 GC 后求和；
2. `spillLocal` 体内构造 `[8]*item` 指针数组，通过间接函数调用 `gcMid`（传入的 `gcStress`）强制数组/局部指针跨调用点存活在栈槽中，逐元素 GC 后读取。

caller 在返回后再次 GC，逐项校验 10 个节点、accum keep 与数组元素。

## 期望行为

1. `mixABI` 返回等于「每轮 probe 贡献 + 全部 p.v + keep.v」的期望值，两次 GC 结果稳定；
2. 10 个指针参数的 `v`/`next.v`/name 在 callee GC 后与 caller GC 后均正确；
3. `[10]*item`、`[4]string` 数组中各槽位未错位；
4. `spillLocal` 返回 8 个节点的 (v + next.v) 之和，跨 8 次间接 GC 调用正确；
5. `acc.total` 与 `acc.keep` 跨 GC 保持；
6. 最后一行 `ALL PASS l17_spill_local_pressure`，退出码 0。

## 失败判读

- `mixABI: spilled arg pN corrupted`：混合 ABI 的溢出槽偏移或指针位图错误；
- `mixABI got=... want=...`：溢出槽中指针被 GC 回收/错位导致求和偏差；
- `spillLocal: ps[N] corrupted`：函数体内指针数组未被栈映射标记；
- `accum corrupted`：receiver/指针参数跨调用丢失；
- 仅 clobberfree/efence 下失败：UAF 已发生但默认堆未复用；
- 崩溃：spill 槽或间接调用点的栈映射错误。

## 参数

- `UAF_SEED`：决定每轮 base，失败输出带种子；
- 无 TORTURE 参数，6 轮固定。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_spill_local_pressure/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
