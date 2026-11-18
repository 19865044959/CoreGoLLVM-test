---
id: l14_concurrent_panic_recover
dim: L14_debug_info
priority: P2
sources:
  - cases/L14_debug_info/l14_concurrent_panic_recover/main.go:79 并发 panic 链叶子
  - cases/L14_debug_info/l14_concurrent_panic_recover/main.go:85 每 goroutine recover 抓栈
  - docs/DIMENSIONS.md:22 L14 调试信息（多 goroutine 栈展开）
tags: [L14, concurrent, panic, recover, debug.Stack, goroutine, TORTURE_ROUNDS, gc]
requires: []
runtime: "<10s（默认 1 轮 × 16 goroutine；TORTURE_ROUNDS 放大轮数，上限 32）"
---

# 场景

每轮并发启动 16 个 goroutine，每个都沿 `prTop→prMid→prLeaf` 链 panic 自己的 payload，在各自 defer 中 recover 并抓 `debug.Stack()`，把栈和 payload 指针回传给 main。模拟高并发服务中多个请求同时 panic 时的栈采集。`TORTURE_ROUNDS` 放大轮数。

## 期望行为

1. 16 份结果按 id 齐全，recover 的 payload 指针与 panic 时同一对象（`q == p`），节点链 id+1 正确；
2. 每份栈都包含 `main.prLeaf(`、`main.prMid(`、`main.prTop(`、`goroutine` 头与 `main.go:<行号>`；
3. 每轮结束后 gcStress，所有 recover 到的 payload 指针在 GC 后仍保持 id/tag/next 正确；
4. 输出不打印栈内容，结果确定；
5. 退出码 0，最后一行 `ALL PASS l14_concurrent_panic_recover`。

## 失败判读

- 某 goroutine 结果缺失或 id 错位：结果通道/并发逻辑问题；
- 栈符号缺失：并发 panic 时 traceback 展开丢失帧；
- GC 后 payload 损坏：recover 后指针未被栈/寄存器正确标记；
- 超时：并发死锁。

## 参数

- `TORTURE_ROUNDS`：轮数，默认 1，范围 1..32；每轮 16 goroutine；
- `UAF_SEED` 不参与逻辑。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_concurrent_panic_recover/*.go
TORTURE_ROUNDS=8 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
